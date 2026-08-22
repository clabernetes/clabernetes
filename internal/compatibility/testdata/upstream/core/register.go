package core

import (
	bar "github.com/srl-labs/containerlab/nodes/bar"
	foo "github.com/srl-labs/containerlab/nodes/foo"
)

func (c *CLab) RegisterNodes() {
	foo.Register(c.Reg)
	bar.Register(c.Reg)
}
