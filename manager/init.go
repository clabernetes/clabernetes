package manager

import (
	"context"
)

func (c *clabernetes) init(ctx context.Context) {
	c.logger.Info("begin init...")

	c.leaderCtx = ctx

	c.logger.Info("initializing crds...")

	err := initializeCrds(c)
	if err != nil {
		c.logger.Fatalf("failed initializing crds, err: %s", err)
	}

	c.logger.Debug("initializing crds complete...")

	c.logger.Info("initializing global config...")

	initializeConfig(c)

	c.logger.Debug("initializing global config complete...")

	c.logger.Info("init complete...")

	c.baseCtxCancel()
}
