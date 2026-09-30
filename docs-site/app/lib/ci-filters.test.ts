import fs from 'node:fs';
import picomatch from 'picomatch';
import { parse } from 'yaml';
import { describe, expect, it } from 'vitest';

const filters = parse(
  fs.readFileSync(new URL('../../../.github/filters.yaml', import.meta.url), 'utf8'),
) as Record<string, string[]>;

// Match paths-filter's some-with-excludes predicate, including dotfiles.
function matches(group: string, files: string[]) {
  const rules = filters[group];
  const include = picomatch(rules.filter((rule) => !rule.startsWith('!')), { dot: true });
  const exclude = picomatch(
    rules.filter((rule) => rule.startsWith('!')).map((rule) => rule.slice(1)),
    { dot: true },
  );
  return files.some((file) => include(file) && !exclude(file));
}

describe('CI file filters', () => {
  it.each([
    'docs/getting-started.md',
    'docs/images/topology.svg',
    'docs-site/app/routes/docs.tsx',
    'docs-site/pnpm-lock.yaml',
    'docs-site/.gitignore',
    'README.md',
    'wrangler.toml',
    '.github/workflows/docs-preview.yaml',
    '.github/workflows/docs-main-preview.yaml',
  ])('runs only docs jobs for %s', (file) => {
    expect(matches('code', [file])).toBe(false);
    expect(matches('docs', [file])).toBe(true);
  });

  it.each([
    'controllers/topology.go',
    'go.mod',
    'go.sum',
    'charts/clabernetes/values.yaml',
    'e2e/fixtures/topology.yaml',
    'build/manager.Dockerfile',
    '.dockerignore',
    '.github/workflows/e2e.yaml',
    'new-component/main.go',
  ])('preserves code CI for %s', (file) => {
    expect(matches('code', [file])).toBe(true);
    expect(matches('docs', [file])).toBe(false);
  });

  it.each([
    'assets/crd/c9s.run_nodes.yaml',
    'Makefile',
    '.mk/tools.mk',
    '.gitattributes',
    '.github/filters.yaml',
    '.github/workflows/file-changes.yaml',
    '.github/workflows/cicd.yaml',
  ])('runs both groups for shared input %s', (file) => {
    expect(matches('code', [file])).toBe(true);
    expect(matches('docs', [file])).toBe(true);
  });

  it('runs both groups for mixed changes', () => {
    const files = ['docs/index.md', 'controllers/topology.go'];
    expect(matches('code', files)).toBe(true);
    expect(matches('docs', files)).toBe(true);
  });

  it('runs neither group for an empty diff', () => {
    expect(matches('code', [])).toBe(false);
    expect(matches('docs', [])).toBe(false);
  });
});
