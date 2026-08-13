## 1. Config API and Bootstrap

- [x] 1.1 Add the validated Config API field and regenerate CRD, OpenAPI, and client artifacts.
- [x] 1.2 Expose the value through the Config manager and preserve Helm bootstrap merge and overwrite semantics with focused tests.
- [x] 1.3 Add the quoted Helm value, JSON schema validation, and chart golden coverage.

## 2. Launcher Deployment Rendering

- [x] 2.1 Resolve one effective CRI kind from the override and cluster detection for CRI-dependent Deployment rendering.
- [x] 2.2 Render the configured directory as a read-only HostPath at its original and conventional nerdctl paths only for enabled containerd pull-through.
- [x] 2.3 Cover custom/default paths, disabled pull-through, invalid paths, non-containerd CRIs, and explicit containerd overrides.

## 3. Documentation

- [x] 3.1 Document Config usage, both mount locations, node path requirements, and certificate path limitations in the image-pull guide.

## 4. Verification

- [x] 4.1 Run focused and full non-e2e Go tests, chart rendering tests, formatting, lint, and whitespace checks.
- [x] 4.2 Run generated-artifact verification and documentation validation.
- [x] 4.3 Validate the OpenSpec change and confirm every changed file belongs to the feature.
