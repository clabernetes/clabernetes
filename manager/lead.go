package manager

import (
	"fmt"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesmanagerelection "github.com/clabernetes/clabernetes/manager/election"
)

func (c *clabernetes) startInitLeaderElection() {
	leaderElectionIdentity := clabernetesmanagerelection.GenerateLeaderIdentity()
	leaderElectionLockName := fmt.Sprintf("%s-init", c.appName)

	leaderElectionLock := clabernetesmanagerelection.GetLeaseLock(
		c.kubeClient,
		c.appName,
		c.namespace,
		leaderElectionLockName,
		leaderElectionIdentity,
	)

	c.logger.Info("start init leader election")
	clabernetesmanagerelection.RunElection(
		c.baseCtx,
		leaderElectionIdentity,
		leaderElectionLock,
		clabernetesmanagerelection.Timers{
			Duration:      electionDuration * time.Second,
			RenewDeadline: electionRenew * time.Second,
			RetryPeriod:   electionRetry * time.Second,
		},
		c.init,
		c.stopLeading,
		c.newLeader,
	)
}

func (c *clabernetes) startLeaderElection() {
	c.leaderElectionIdentity = clabernetesmanagerelection.GenerateLeaderIdentity()
	leaderElectionLockName := fmt.Sprintf("%s-manager", c.appName)

	leaderElectionLock := clabernetesmanagerelection.GetLeaseLock(
		c.kubeClient,
		c.appName,
		c.namespace,
		leaderElectionLockName,
		c.leaderElectionIdentity,
	)

	c.logger.Info("start leader election")

	clabernetesmanagerelection.RunElection(
		c.baseCtx,
		c.leaderElectionIdentity,
		leaderElectionLock,
		clabernetesmanagerelection.Timers{
			Duration:      electionDuration * time.Second,
			RenewDeadline: electionRenew * time.Second,
			RetryPeriod:   electionRetry * time.Second,
		},
		c.startLeading,
		c.stopLeading,
		c.newLeader,
	)
}

func (c *clabernetes) stopLeading() {
	c.logger.Info("stopping clabernetes...")

	c.Exit(clabernetesconstants.ExitCode)
}

func (c *clabernetes) newLeader(newLeaderIdentity string) {
	c.logger.Infof("new leader elected '%s'", newLeaderIdentity)

	if newLeaderIdentity != c.leaderElectionIdentity {
		c.logger.Debug(
			"new leader is not us, nothing else for us to do. setting ready state to true",
		)

		c.ready = true
	} else {
		c.logger.Debug("new leader is us, resetting ready state to false")

		c.ready = false
	}
}
