Architecture
============

# Overview

clabernetes itself is a collection of kubernetes custom resources and controllers that reconcile 
those resources. The ultimate goal of the controllers is to, based on custom resource 
definitions, render a network topology in the cluster. This "rendering" includes appropriately 
stitching network node interfaces together, and exposing the management interfaces of the nodes 
in the topology.

There are currently two custom resource types: "containerlab", and "kne". Each resource type 
accepts some common arguments and a "native" configuration/topology file from the given project 
(containerlab/kne).


## Components

### Controller & Custom Resource Definitions

The "brains" of clabernetes is the controller -- the controller is simply a deployment that is 
installed into your kubernetes cluster. The controller is great, but without the Custom Resource 
Definitions (CRDs), it has nothing to do! clabernetes CRDs define the topologies you want to 
create. Once you create a CR from one of the clabernetes CRDs, the controller takes action and 
begins to reconcile what currently exists in the cluster versus what *should* exist based on 
your CR.

The controller itself is simply a go program built around the controller-runtime project -- this 
program runs inside a standard kubernetes deployment which must be installed into your cluster.


### Clabverter

While the goal of clabernetes is to take a containerlab or kne topology and "directly" translate 
it into a running clabernetes topology in your cluster, there are a few things that cannot be 
translated directly. Chief among those is startup configurations or any other type of file that 
you would like to mount to some path on one of your nodes. Containerlab and kne both solve this 
problem by letting you run binaries on your local machine and then mounting/copying files 
relative to where you ran the command into their appropriate location(s).

As clabernetes is not running on your machine, and you only interact with it via the kubernetes 
api (typically via kubectl, but could be curl or whatever too!), we don't have any way to 
automagically copy or mount any files from your machine.

To work around this, the "clabverter" tool was created -- this is a very simple cli tool that 
can be pointed at a (for now only) containerlab topology (either locally or at a URL). This tool 
then determines if any files would be mounted when using this topology file, if "yes", it will 
render kubernetes configmaps containing the file contents, and generate a clabernetes 
Containerlab CR that appropriately mounts the configmaps such that the files will be mounted in 
the pod once it is running.


## Topologies

The whole point of clabernetes is to deploy a containerlab or kne topology into kubernetes (in a 
"standard" clabernetes way, obviously kne can already do this on its own) -- so, what does that 
actually look like? Great question! The following sections outline the high level bits...


 Once this node is up, the 
launcher then handles connecting the node as defined in the original topology file.




### Nodes

Regardless of the flavor of topology you want to deploy (containerlab/kne), clabernetes will
deploy a single *Deployment per Node* -- that means a single kubernetes Deployment per
containerlab/kne node (not kubernetes node!). Why? Simply because this is the easiest way to do
things really. With a single Deployment representing a single node we can treat every node in
the same way -- connectivity is the same, exposing the node is the same, deployments (mostly)
are the same, etc..

Each Deployment runs a single container in the pod -- that container is a Debian image that
contains the clabernetes launcher binary, and has docker installed in the container. On startup
the clabernetes launcher fetches "its" `Node` custom resource -- the controller renders one Node
resource per node of the topology holding that node's "sub-topology" -- then handles any initial
setup, and finally launches "normal" containerlab with a topology file representing *one node
from the original topology*. Storing each node's config as its own (small) object means even
very large topologies never bump into the etcd max object size.

**Note:** that this is not "normal" docker-in-docker as we aren't actually mounting the docker sock
in the container -- this is a full-blown docker installation independent of the CRI of your cluster.
This is obviously not ideal, *but* means we are free to do whatever we want without having to
mess with the host clusters CRI or CNI.


### Inter-Node Connectivity

After the launcher has taken care of spinning up the node it then checks to see if this node
requires any connectivity to other nodes in the topology. The launcher gets this information
courtesy of the controllers -- they already broke up the topology into the node per Deployment
setup outlined -- the controller also creates one `Link` custom resource per point-to-point
link between launchers. Each launcher lists (and then watches) the Link resources terminating
on its own node and, once again thanks to containerlab giving a nice helping hand here, handles
the connectivity via VXLAN tunnels.


### Exposing Nodes

Lastly, the nodes of course need to be exposed somehow so you can connect to them with SSH or 
NETCONF or whatever. The controller handles this part by creating kubernetes Service(s) of the 
LoadBalancer flavor. You can check the status field of your CR to find the IP assigned for each 
node's LoadBalancer Service, or you can check via normal kubernetes means.
