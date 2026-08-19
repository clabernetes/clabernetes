---
title: CRD reference
description: Interactive field reference for c9s custom resources.
icon: Braces
---

Schemas below are generated from the controller CRD YAML in `assets/crd/`. Node and Link are the
primary API; Topology is a supported higher-level compatibility resource that compiles into
LauncherProfile, Link, and Node objects.

## Resources

- [Topology](/docs/crd/topology) — optional whole-lab compiler input
- [Node](/docs/crd/node) — primary API: one containerlab node per object
- [Link](/docs/crd/link) — primary API: one wire between two nodes
- [LauncherProfile](/docs/crd/launcher-profile) — reusable launcher policy
- [Config](/docs/crd/config) — cluster-wide defaults
