// Package deviceplan converts imported containerlab kind intent into c9s-owned runtime-neutral
// plans.
package deviceplan

import (
	clabcore "github.com/srl-labs/containerlab/core"
	clabnodes "github.com/srl-labs/containerlab/nodes"
)

// NewContainerlabRegistry constructs the authoritative registry exported by the pinned
// containerlab dependency. Deliberately avoid core.NewContainerLab: registry construction does not
// need topology parsing, host DNS discovery, or runtime initialization.
func NewContainerlabRegistry() *clabnodes.NodeRegistry {
	registry := clabnodes.NewNodeRegistry()
	lab := &clabcore.CLab{Reg: registry}
	lab.RegisterNodes()

	return registry
}
