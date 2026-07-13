Architecture
============

# Overview

clabernetes is a collection of kubernetes custom resources and controllers that reconcile those
resources. The ultimate goal of the controllers is to render a network topology in the cluster:
one launcher pod per (containerlab) node, network node interfaces stitched together across pods,
and the management interfaces of the nodes exposed.

The model in one picture:

```
                     ┌──────────────────────────────────────────────┐
   OPTIONAL          │  Topology CR  (compiler / convenience layer) │
                     │  containerlab yaml in → emits Node+Link CRs  │
                     └──────────────┬───────────────────────────────┘
                                    │ emits (owns, prunes, aggregates status)
                                    ▼
   AUTHORITATIVE     Node CRs (1 per launcher pod)     Link CRs (1 per wire)
   API               created by: user | tooling | Topology compiler
                                    │                        │
                     ┌──────────────┴─────────┐   ┌──────────┴─────────┐
                     │ Node controller        │   │ Link controller    │
                     │ deployment, services,  │   │ validates, allocs  │
                     │ PVC, digests, status   │   │ tunnelID → status  │
                     └──────────────┬─────────┘   └──────────┬─────────┘
                                    ▼                        ▼
                     launcher pod: fetches its Node, field-selector-watches its
                     Links, materializes topo.clab.yaml, runs clab + tunnels
```

Everything below the "AUTHORITATIVE API" line works identically whether the custom resources
were written by a person, by tooling, or by the Topology compiler.


## The Primary API: Nodes and Links

A `Node` custom resource represents a single containerlab node -- its spec is simply what a
human would write for that node in a containerlab topology file (flat, verbatim containerlab
vocabulary). A `Link` custom resource represents a single wire between two nodes. That's the
whole authoritative api: apply Node and Link objects and the controllers render a running,
wired lab -- no Topology object required.

The object name of a Node *is* the containerlab node name: the launcher pod hostname and the
node's services derive from it, which also means the **namespace is the topology boundary**.
Everything operational is controller-stamped into the statuses: expose port allocations and
readiness on Nodes, tunnel id allocations on Links. Deployment *policy* -- expose behavior,
image pull config, launcher resources, scheduling, privileges -- lives on `NodeProfile`
objects that select Nodes by label, so whoever (or whatever) emits Nodes never needs to know
about deployment policy.

A deliberate scale property falls out of this design: no persisted object grows with the size
of the topology. A Node grows only with its own definition, a Link is O(1), and each launcher
watches exactly the links terminating on its own nodes (server side field selectors, which is
why clabernetes requires kubernetes 1.31+).


## Components

### Controllers & Custom Resource Definitions

The "brains" of clabernetes is the manager deployment which runs three cooperating controllers:

- the **node controller** turns every (launcher) Node into a deployment, a per-node "fabric"
  service (`<name>-vx`, the tunnel termination point), an expose service (`<name>`), and an
  optional PVC -- and stamps readiness/allocations into the Node status. Grouped nodes
  (containerlab's `network-mode: container:<primary>`) share their primary's pod.
- the **link controller** validates Links and allocates tunnel ids into their statuses.
- the **topology controller** is the optional convenience layer: it *compiles* a Topology
  (a containerlab or kne file plus knobs) into Node/Link/NodeProfile objects -- expanding
  topology defaults/kinds into each node so every emitted Node is self contained -- prunes
  emitted objects that fall out of the definition, protects them from drift, and aggregates
  node readiness back into the Topology status.

### Launchers

Each Node gets a Deployment running a single launcher container -- a Debian image with the
clabernetes launcher binary and a full docker installation (not docker-in-docker: no docker
sock mounting, just an independent docker inside the pod, free of the cluster's CRI/CNI).

On startup the launcher fetches its own Node object (and those of any grouped nodes), lists
the Links terminating on them via field selectors, and verifies that link view against a
digest annotation the node controller stamped on the pod. From that it materializes a
containerlab topology file locally and runs plain containerlab.

### Inter-Node Connectivity

Cross-pod wires are realized as vxlan (or, experimentally, slurpeeth) tunnels between the
per-node fabric services. The tunnel destination is *derived* from the link spec alone
(`<remote node>-vx.<namespace>...`), and the launcher keeps watching its links: moving a
wire's far end ("rewiring") re-targets the tunnel live without restarting anything, while
changing the set of interfaces attached to a node rolls just that node's pod.

### Exposing Nodes

Ports listed in a node definition (plus a sensible default set unless auto-expose is
disabled) are allocated into the Node status and exposed through a per-node service --
LoadBalancer flavored by default. The assigned address is reflected back into the Node
status.

### Clabverter

While the goal of clabernetes is to take a containerlab topology and "directly" translate it
into a running clabernetes topology in your cluster, there are a few things that cannot be
translated directly. Chief among those is startup configurations or any other type of file
that you would like to mount to some path on one of your nodes. Containerlab solves this
problem by letting you run binaries on your local machine and then mounting/copying files
relative to where you ran the command into their appropriate location(s).

As clabernetes is not running on your machine, and you only interact with it via the
kubernetes api, we don't have any way to automagically copy or mount any files from your
machine.

To work around this, the "clabverter" tool was created -- this is a very simple cli tool that
can be pointed at a containerlab topology (either locally or at a URL). This tool determines
if any files would be mounted when using this topology file, and if so renders kubernetes
configmaps containing the file contents and a Topology that appropriately mounts them into
the pods.

clabverter can also skip the Topology object entirely: `--emit-crs` renders the primitive
Node/Link/NodeProfile manifests directly, using the very same compile pipeline the in-cluster
compiler runs -- handy when you want the primary api objects in a git repo rather than an
in-cluster compiler owning them.
