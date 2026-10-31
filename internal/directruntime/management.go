package directruntime

// PodAddressEnvironmentVariable carries the Pod's own cluster address into every direct
// application container and connectivity helper through the Kubernetes downward API. It is the
// Pod's Kubernetes transport identity; management identities are controller-allocated and never
// derived from it.
const PodAddressEnvironmentVariable = "C9S_POD_ADDRESS"
