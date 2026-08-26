package http

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	"k8s.io/client-go/kubernetes"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	managerInstance     Manager   //nolint:gochecknoglobals
	managerInstanceOnce sync.Once //nolint:gochecknoglobals
)

const (
	timeoutSeconds = 5
	listenPort     = 10443
)

// InitManager initializes the clabernetes http server manager -- it does this once only, its a
// no-op if the manager is already initialized. This accepts the base context cancel func to cancel
// it if there is any issue w/ the http sever.
func InitManager(
	c clabernetesmanagertypes.Clabernetes,
) {
	managerInstanceOnce.Do(func() {
		logManager := claberneteslogging.GetManager()

		logger := logManager.MustRegisterAndGetLogger(
			"http-manager",
			clabernetesutil.GetEnvStrOrDefault(
				clabernetesconstants.ManagerLoggerLevelEnv,
				clabernetesconstants.Info,
			),
		)

		m := &manager{
			ctx:           c.GetContext(),
			ctxCancel:     c.GetContextCancel(),
			logger:        logger,
			managerReadyF: c.IsReady,
			client:        c.GetCtrlRuntimeClient(),
			kubeClient:    c.GetKubeClient(),
		}

		managerInstance = m
	})
}

// GetManager returns the config manager -- if the manager has not been initialized it panics.
func GetManager() Manager {
	if managerInstance == nil {
		panic(
			"http manager instance is nil, 'GetManager' should never be called until the " +
				"manager process has been started",
		)
	}

	return managerInstance
}

// Manager is the http server manager interface defining the server manager methods.
type Manager interface {
	// Start starts the http server.
	Start()
	// Stop stops the http server by calling the http.Server.Shutdown() method.
	Stop() error
}

type manager struct {
	ctx           context.Context
	ctxCancel     context.CancelFunc
	logger        claberneteslogging.Instance
	managerReadyF func() bool
	returnedReady bool
	client        ctrlruntimeclient.Client
	kubeClient    *kubernetes.Clientset
	server        *http.Server
	stopping      bool
}

func (m *manager) Start() {
	mux := http.NewServeMux()

	// register endpoints
	mux.HandleFunc(
		aliveRoute,
		m.aliveHandler,
	)

	m.server = &http.Server{
		BaseContext: func(_ net.Listener) context.Context {
			return m.ctx
		},
		Addr: fmt.Sprintf(
			":%d",
			listenPort,
		),
		Handler:           mux,
		ReadTimeout:       timeoutSeconds * time.Second,
		WriteTimeout:      timeoutSeconds * time.Second,
		ReadHeaderTimeout: timeoutSeconds * time.Second,
	}

	go func() {
		err := m.server.ListenAndServe()
		if err != nil && !m.stopping {
			m.logger.Criticalf("http manager server has failed, error: %s", err)

			m.ctxCancel()
		}
	}()
}

func (m *manager) Stop() error {
	m.stopping = true

	return m.server.Shutdown(m.ctx)
}

func (m *manager) logRequest(r *http.Request) {
	m.logger.Debugf("received %q on %q endpoint from %q", r.Method, r.RequestURI, r.RemoteAddr)
}
