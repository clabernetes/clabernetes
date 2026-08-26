package manager

// prepare handles preparation tasks that happen before running the clabernetes.start method.
func (c *clabernetes) prepare() {
	c.logger.Info("begin prepare...")

	c.logger.Info("preparing scheme...")

	err := registerToScheme(c)
	if err != nil {
		c.logger.Fatalf("failed registering apis to scheme, err: %s", err)
	}

	c.logger.Debug("preparing scheme complete...")
}
