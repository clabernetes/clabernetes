[![Discord](https://img.shields.io/discord/860500297297821756?style=flat-square&label=discord&logo=discord&color=00c9ff&labelColor=bec8d2)](https://discord.gg/vAyddtaEV9)
[![Go Report](https://img.shields.io/badge/go%20report-A%2B-blue?style=flat-square&color=00c9ff&labelColor=bec8d2)](https://goreportcard.com/report/github.com/srl-labs/clabernetes)

# clabernetes a.k.a c9s

Love containerlab? Want containerlab, just distributed in a kubernetes cluster? Enter
clabernetes -- containerlab + kubernetes. clabernetes is a kubernetes controller that deploys valid
containerlab topologies into a kubernetes cluster.

See [clabernetes docs](https://containerlab.dev/manual/clabernetes) for reference.

## Deploy

Deploying this chart is like deploying any other helm chart! The simplest case looks something like:

```bash
helm upgrade --install clabernetes oci://ghcr.io/srl-labs/clabernetes/clabernetes
```

You can select to install a specific version of the chart by adding the `--version` flag -- you can
find all the versions of the chart stored as a package on the projects GitHub page
[here](https://github.com/srl-labs/clabernetes/pkgs/container/clabernetes%2Fclabernetes).

## Upgrade and rollback

Manager and launcher images from different released chart versions may use incompatible internal
APIs. During an upgrade, an old official launcher tag persisted in the global Config or a Topology
is therefore resolved to the launcher bundled with the running manager. The persisted Config value
is not rewritten: rolling the chart back lets the older manager select its original launcher and
recreate its legacy resources.

Nonstandard launcher images are treated as explicit overrides and are not changed automatically.
Before upgrading, custom launcher images must be rebuilt from the target clabernetes release.

## Values

As with most helm charts, this chart is configurable via values -- please refer to the charts
default values file for reference. You can find it
[here](https://github.com/srl-labs/clabernetes/blob/main/charts/clabernetes/values.yaml) or on the [Artifact Hub](https://artifacthub.io/packages/helm/clabernetes/clabernetes?modal=values).
