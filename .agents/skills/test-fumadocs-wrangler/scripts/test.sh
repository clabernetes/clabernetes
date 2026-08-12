#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
DOCS_SITE="$REPO_ROOT/docs-site"
PORT="${PORT:-8787}"
TMP_BASE="${TMPDIR:-/tmp}"
TMP_ROOT="$(mktemp -d "$TMP_BASE/fumadocs-wrangler-test.XXXXXX")"
TOOLS_DIR="$TMP_ROOT/tools"
BROWSERS_DIR="$TMP_ROOT/browsers"
WRANGLER_PID=""

cleanup() {
  if [[ -n "$WRANGLER_PID" ]] && kill -0 "$WRANGLER_PID" 2>/dev/null; then
    kill "$WRANGLER_PID" 2>/dev/null || true
    wait "$WRANGLER_PID" 2>/dev/null || true
  fi

  case "$TMP_ROOT" in
    "$TMP_BASE"/fumadocs-wrangler-test.*)
      rm -rf -- "$TMP_ROOT"
      ;;
    *)
      printf 'Refusing to remove unexpected temporary path: %s\n' "$TMP_ROOT" >&2
      ;;
  esac
}
trap cleanup EXIT INT TERM

if [[ ! -d "$DOCS_SITE/node_modules" ]]; then
  printf 'docs-site/node_modules is missing; install project dependencies separately.\n' >&2
  exit 1
fi

if (($# == 0)); then
  SPECS=("/docs")
else
  SPECS=("$@")
fi

printf 'Building docs...\n'
pnpm --dir "$DOCS_SITE" run build

printf 'Installing test tools under %s...\n' "$TMP_ROOT"
mkdir -p "$TOOLS_DIR" "$BROWSERS_DIR" "$TMP_ROOT/docs-site/build"
NPM_CONFIG_CACHE="$TMP_ROOT/npm-cache" \
  npm install --prefix "$TOOLS_DIR" --no-audit --no-fund --silent \
  playwright@latest wrangler@4

PLAYWRIGHT="$TOOLS_DIR/node_modules/.bin/playwright"
WRANGLER="$TOOLS_DIR/node_modules/.bin/wrangler"
PLAYWRIGHT_BROWSERS_PATH="$BROWSERS_DIR" "$PLAYWRIGHT" install chromium

cp "$REPO_ROOT/wrangler.toml" "$TMP_ROOT/wrangler.toml"
ln -s "$DOCS_SITE/build/client" "$TMP_ROOT/docs-site/build/client"

export XDG_CONFIG_HOME="$TMP_ROOT/xdg-config"
export WRANGLER_HOME="$TMP_ROOT/wrangler-home"
export WRANGLER_SEND_METRICS=false
export PLAYWRIGHT_BROWSERS_PATH="$BROWSERS_DIR"

(
  cd "$TMP_ROOT"
  "$WRANGLER" dev \
    --config "$TMP_ROOT/wrangler.toml" \
    --port "$PORT" \
    --persist-to "$TMP_ROOT/wrangler-state"
) >"$TMP_ROOT/wrangler.log" 2>&1 &
WRANGLER_PID=$!

BASE_URL="http://127.0.0.1:$PORT"
ready=false
for _ in {1..120}; do
  if curl --fail --silent --output /dev/null "$BASE_URL/"; then
    ready=true
    break
  fi
  if ! kill -0 "$WRANGLER_PID" 2>/dev/null; then
    break
  fi
  sleep 0.25
done

if [[ "$ready" != true ]]; then
  printf 'Wrangler failed to start:\n' >&2
  while IFS= read -r line; do
    printf '%s\n' "$line" >&2
  done <"$TMP_ROOT/wrangler.log"
  exit 1
fi

TEST_SPECS_JSON="$(
  node -e 'process.stdout.write(JSON.stringify(process.argv.slice(1)))' "${SPECS[@]}"
)"
export BASE_URL TEST_SPECS_JSON TOOLS_DIR

node <<'NODE'
const { chromium } = require(
  `${process.env.TOOLS_DIR}/node_modules/playwright`,
);

const baseUrl = process.env.BASE_URL;
const specs = JSON.parse(process.env.TEST_SPECS_JSON);

async function verify(page, expectedPath, errors, phase) {
  await page.waitForLoadState('networkidle').catch(() => {});
  await page.waitForTimeout(250);

  const actualPath = new URL(page.url()).pathname;
  const body = await page.locator('body').innerText();
  const problems = [...errors];

  if (actualPath !== expectedPath) {
    problems.push(`expected path ${expectedPath}, got ${actualPath}`);
  }
  if (!body.trim()) {
    problems.push('page body is empty');
  }
  if (
    body.includes('Something went wrong') ||
    body.includes('Documentation page not found')
  ) {
    problems.push('application error page rendered');
  }

  if (problems.length > 0) {
    throw new Error(`${phase} failed:\n  ${problems.join('\n  ')}`);
  }
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  let failed = false;

  for (const spec of specs) {
    const separator = spec.indexOf('=>');
    const source =
      separator === -1 ? spec : spec.slice(0, separator);
    const expected =
      separator === -1 ? source : spec.slice(separator + 2);
    const page = await browser.newPage();
    const errors = [];

    page.on('console', (message) => {
      if (message.type() === 'error') {
        errors.push(`console: ${message.text()}`);
      }
    });
    page.on('pageerror', (error) => {
      errors.push(`page: ${error.message}`);
    });
    page.on('requestfailed', (request) => {
      errors.push(
        `request: ${request.url()} (${request.failure()?.errorText ?? 'failed'})`,
      );
    });

    try {
      await page.goto(`${baseUrl}${source}`, {
        waitUntil: 'domcontentloaded',
      });
      if (source !== expected) {
        await page.waitForURL((url) => url.pathname === expected);
      }
      await verify(page, expected, errors, `direct load ${spec}`);

      errors.length = 0;
      await page.reload({ waitUntil: 'domcontentloaded' });
      await verify(page, expected, errors, `refresh ${spec}`);

      console.log(`PASS ${spec}`);
    } catch (error) {
      failed = true;
      console.error(`FAIL ${spec}\n${error.stack ?? error}`);
    } finally {
      await page.close();
    }
  }

  await browser.close();
  if (failed) process.exit(1);
})().catch((error) => {
  console.error(error.stack ?? error);
  process.exit(1);
});
NODE

if [[ "${DEPLOY_TEMPORARY:-0}" == "1" ]]; then
  printf 'Uploading passing build to a temporary Cloudflare account...\n'
  (
    cd "$TMP_ROOT"
    "$WRANGLER" deploy \
      --temporary \
      --config "$TMP_ROOT/wrangler.toml"
  )
fi
