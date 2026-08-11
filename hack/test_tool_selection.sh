#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fake_bin=$(mktemp -d)
trap 'rm -rf "$fake_bin"' EXIT

cat > "${fake_bin}/gh" <<'EOF'
#!/usr/bin/env bash
printf 'fake host gh must not be invoked\n' >&2
exit 99
EOF
cat > "${fake_bin}/helm" <<'EOF'
#!/usr/bin/env bash
printf 'fake host helm must not be invoked\n' >&2
exit 99
EOF
cat > "${fake_bin}/kubectl" <<'EOF'
#!/usr/bin/env bash
printf 'fake host kubectl must not be invoked\n' >&2
exit 99
EOF
chmod +x "${fake_bin}"/gh "${fake_bin}"/helm "${fake_bin}"/kubectl

output=$(
    cd "${repo_root}"
    PATH="${fake_bin}:${PATH}" make -n ls-releases
)

printf '%s\n' "${output}" | rg -q 'build/try-c9s/bin/gh-v[0-9]'
printf '%s\n' "${output}" | rg -q 'build/try-c9s/bin/helm-v[0-9]'
if printf '%s\n' "${output}" | rg -q 'fake host (gh|helm|kubectl)'; then
    printf 'host tool was selected for ls-releases\n' >&2
    exit 1
fi

printf 'repository-local tool selection check passed\n'
