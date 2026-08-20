//nolint:wsl_v5 // Manager bootstrap keeps closely related configuration assignments together.
package manager

import (
	"context"
	"math/rand"
	"os"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteshttp "github.com/clabernetes/clabernetes/http"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	electionDuration = 60
	electionRenew    = 40
	electionRetry    = 8
)

// StartClabernetes is a function that starts the clabernetes manager.
func StartClabernetes(initializer bool) {
	if clabernetesInstance != nil {
		clabernetesutil.Panic("clabernetes instance already created...")
	}

	rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec

	claberneteslogging.InitManager()

	logManager := claberneteslogging.GetManager()

	clabernetesLogger := logManager.MustRegisterAndGetLogger(
		clabernetesconstants.Clabernetes,
		clabernetesutil.GetEnvStrOrDefault(
			clabernetesconstants.ManagerLoggerLevelEnv,
			clabernetesconstants.Info,
		),
	)

	err := createNewKlogLogger(logManager)
	if err != nil {
		clabernetesLogger.Fatalf("failed patching klog, err: %s", err)
	}

	// set controller-runtime log too; we just use klogr since we'll patch it to be wrapped in our
	// log manager anyway
	ctrlruntimelog.SetLogger(klog.NewKlogr())

	ctx, cancel := clabernetesutil.SignalHandledContext(clabernetesLogger.Criticalf)

	clabernetesInstance = &clabernetes{
		baseCtx:       ctx,
		baseCtxCancel: cancel,
		appName: clabernetesutil.GetEnvStrOrDefault(
			clabernetesconstants.AppNameEnv,
			clabernetesconstants.AppNameDefault,
		),
		initializer:        initializer,
		logger:             clabernetesLogger,
		deviceRuntimeImage: os.Getenv(clabernetesconstants.DeviceRuntimeImageEnv),
	}

	clabernetesInstance.start()
}

var clabernetesInstance *clabernetes //nolint:gochecknoglobals

type clabernetes struct {
	baseCtx       context.Context
	baseCtxCancel context.CancelFunc
	leaderCtx     context.Context

	appName string

	initializer bool

	logger claberneteslogging.Instance

	namespace string

	kubeConfig            *rest.Config
	kubeClient            *kubernetes.Clientset
	kubeClabernetesClient *clabernetesgeneratedclientset.Clientset

	deviceRuntimeImage string

	scheme *apimachineryruntime.Scheme
	mgr    ctrlruntime.Manager

	leaderElectionIdentity string
	// ready is set to true after controller-runtime caches have been synced and "startup" is
	// complete
	ready bool
}

func (c *clabernetes) GetContext() context.Context {
	return c.baseCtx
}

func (c *clabernetes) GetContextCancel() context.CancelFunc {
	return c.baseCtxCancel
}

func (c *clabernetes) GetAppName() string {
	return c.appName
}

func (c *clabernetes) GetBaseLogger() claberneteslogging.Instance {
	return c.logger
}

func (c *clabernetes) GetNamespace() string {
	return c.namespace
}

func (c *clabernetes) GetDeviceRuntimeImage() string {
	return c.deviceRuntimeImage
}

func (c *clabernetes) IsInitializer() bool {
	return c.initializer
}

func (c *clabernetes) GetKubeConfig() *rest.Config {
	return c.kubeConfig
}

func (c *clabernetes) GetKubeClient() *kubernetes.Clientset {
	return c.kubeClient
}

func (c *clabernetes) GetKubeClabernetesClient() *clabernetesgeneratedclientset.Clientset {
	return c.kubeClabernetesClient
}

func (c *clabernetes) GetScheme() *apimachineryruntime.Scheme {
	return c.scheme
}

func (c *clabernetes) GetCtrlRuntimeMgr() ctrlruntime.Manager {
	return c.mgr
}

func (c *clabernetes) GetCtrlRuntimeClient() ctrlruntimeclient.Client {
	return c.mgr.GetClient()
}

func (c *clabernetes) NewContextWithTimeout() (context.Context, context.CancelFunc) {
	dur := clabernetesconstants.DefaultClientOperationTimeout
	mul := clabernetesutil.GetEnvFloat64OrDefault(
		clabernetesconstants.ClientOperationTimeoutMultiplierEnv,
		1,
	)

	finalDur := time.Duration(dur.Seconds()*mul) * time.Second

	c.logger.Debugf("issuing new context with timeout value '%s'", finalDur)

	if c.leaderCtx == nil {
		c.logger.Info("requesting new context but leader election context has not been set")

		return context.WithTimeout(c.baseCtx, finalDur)
	}

	return context.WithTimeout(c.leaderCtx, finalDur)
}

func (c *clabernetes) IsReady() bool {
	return c.ready
}

func (c *clabernetes) Exit(exitCode int) {
	if !c.initializer {
		// init container would never have started the http server, so we skip shutting it down
		// of course
		err := claberneteshttp.GetManager().Stop()
		if err != nil {
			c.logger.Warnf("failed shutting down http manager, err: %s", err)
		}
	}

	claberneteslogging.GetManager().Flush()

	os.Exit(exitCode)
}

func (c *clabernetes) start() {
	c.logger.Info("starting clabernetes...")

	c.logger.Debugf("clabernetes version %s", clabernetesconstants.Version)

	c.preInit()

	if c.initializer {
		// initializer means we are the init container and should run initialization tasks like
		// creating crds/webhook configs. once done with this we are done and the init process will
		// call os.exit to kill the process.
		c.startInitLeaderElection()

		return
	}

	c.prepare()

	// dont create the manager until we've loaded the scheme!
	var err error

	c.mgr, err = newManager(c.scheme, c.appName)
	if err != nil {
		c.logger.Criticalf("failed creating controller runtime manager, err: %s", err)

		c.Exit(clabernetesconstants.ExitCodeError)
	}

	c.logger.Debug("prepare complete...")

	c.logger.Info("starting http manager...")

	claberneteshttp.InitManager(c)
	claberneteshttp.GetManager().Start()

	c.logger.Debug("http manager started...")

	c.startLeaderElection()
}
