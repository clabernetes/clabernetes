package manager

import (
	"flag"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	"k8s.io/klog/v2"
)

const (
	klogLoggerName      = "klog"
	klogLogToStderr     = "logtostderr"
	klogAlsoLogToStderr = "alsologtostderr"
)

func createNewKlogLogger(logManager claberneteslogging.Manager) error {
	err := logManager.RegisterLogger(klogLoggerName, clabernetesconstants.Info)
	if err != nil {
		return err
	}

	err = logManager.SetLoggerFormatter(klogLoggerName, claberneteslogging.DefaultKlogFormatter)
	if err != nil {
		return err
	}

	klogLogger, err := logManager.GetLogger(klogLoggerName)
	if err != nil {
		return err
	}

	err = patchKlog(klogLogger)
	if err != nil {
		return err
	}

	return nil
}

func patchKlog(klogLogInstance claberneteslogging.Instance) error {
	klog.InitFlags(nil)

	err := flag.Set(klogLogToStderr, clabernetesconstants.False)
	if err != nil {
		return err
	}

	err = flag.Set(klogAlsoLogToStderr, clabernetesconstants.False)
	if err != nil {
		return err
	}

	flag.Parse()

	klog.SetOutput(klogLogInstance)

	return nil
}
