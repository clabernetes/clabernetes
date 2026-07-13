package manager

import (
	"context"

	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	clabernetescontrollers "github.com/srl-labs/clabernetes/controllers"
	clabernetescontrollersimagerequest "github.com/srl-labs/clabernetes/controllers/imagerequest"
	clabernetescontrollerslink "github.com/srl-labs/clabernetes/controllers/link"
	clabernetescontrollerstopology "github.com/srl-labs/clabernetes/controllers/topology"
	clabernetesutil "github.com/srl-labs/clabernetes/util"
)

func (c *clabernetes) startLeading(ctx context.Context) {
	c.leaderCtx = ctx

	c.preStart()

	go func() {
		err := c.mgr.Start(c.leaderCtx)
		if err != nil {
			c.logger.Criticalf(
				"encountered error starting controller-runtime manager, err: %s",
				err,
			)

			c.Exit(clabernetesconstants.ExitCodeError)
		}
	}()

	c.logger.Info("begin syncing controller-runtime manager cache...")

	synced := c.mgr.GetCache().WaitForCacheSync(c.leaderCtx)
	if !synced {
		c.logger.Critical("encountered error syncing controller-runtime manager cache")

		c.Exit(clabernetesconstants.ExitCodeError)
	}

	c.logger.Debug("controller-runtime manager cache synced...")

	c.logger.Info("registering controllers...")

	controllersToRegisterFuncs := []clabernetescontrollers.NewController{
		clabernetescontrollerstopology.NewController,
		clabernetescontrollerslink.NewController,
		clabernetescontrollersimagerequest.NewController,
	}

	for _, newF := range controllersToRegisterFuncs {
		ctrl := newF(c)

		clabernetesutil.MustSetupWithManager(ctrl.SetupWithManager, c.mgr)
	}

	c.logger.Debug("controllers registered...")

	c.ready = true

	c.logger.Debug("startup complete...")

	c.postStart()

	c.logger.Info("running forever or until interrupt...")

	<-c.leaderCtx.Done()
}
