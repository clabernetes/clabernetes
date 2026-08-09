# c9s documentation site

This package contains the Fumadocs application. Documentation content remains in the repository
`docs/` directory and is loaded by the Vite development and static build workflows.

The package uses pnpm independently of the npm-managed operator UI under `ui/`.

From the repository root:

```bash
make serve-docs
```

The Make target installs dependencies from `pnpm-lock.yaml` before starting the development server.

Other workflows:

```bash
make docs-install
make check-docs
make build-docs
make preview-docs
```

The production artifact is written to `docs-site/build/client` and requires only a static file
server.
