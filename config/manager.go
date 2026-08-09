package config

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	"gopkg.in/yaml.v3"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerywatch "k8s.io/apimachinery/pkg/watch"
)

var (
	managerInstance     Manager   //nolint:gochecknoglobals
	managerInstanceOnce sync.Once //nolint:gochecknoglobals
)

// ManagerGetterFunc returns an instance of the config manager.
type ManagerGetterFunc func() Manager

// InitManager initializes the config manager -- it does this once only, its a no-op if the manager
// is already initialized.
func InitManager(
	ctx context.Context,
	appName, namespace string,
	client *clabernetesgeneratedclientset.Clientset,
) {
	managerInstanceOnce.Do(func() {
		logManager := claberneteslogging.GetManager()

		logger := logManager.MustRegisterAndGetLogger(
			"config-manager",
			clabernetesutil.GetEnvStrOrDefault(
				clabernetesconstants.ManagerLoggerLevelEnv,
				clabernetesconstants.Info,
			),
		)

		m := &manager{
			ctx:                   ctx,
			logger:                logger,
			appName:               appName,
			namespace:             namespace,
			kubeClabernetesClient: client,
			lock:                  &sync.RWMutex{},
			config: &clabernetesapisv1alpha1.ConfigSpec{
				InClusterDNSSuffix: clabernetesconstants.KubernetesDefaultInClusterDNSSuffix,
				Metadata: clabernetesapisv1alpha1.ConfigMetadata{
					Annotations: nil,
					Labels:      nil,
				},
				Deployment: clabernetesapisv1alpha1.ConfigDeployment{
					ResourcesDefault: &k8scorev1.ResourceRequirements{
						Limits:   nil,
						Requests: nil,
						Claims:   nil,
					},
					ResourcesByContainerlabKind: make(
						map[string]map[string]*k8scorev1.ResourceRequirements,
					),
					NodeSelectorsByImage:    make(map[string]map[string]string),
					PrivilegedLauncher:      true,
					ContainerlabDebug:       false,
					LauncherImage:           os.Getenv(clabernetesconstants.LauncherImageEnv),
					LauncherImagePullPolicy: clabernetesconstants.KubernetesImagePullIfNotPresent,
					LauncherLogLevel:        clabernetesconstants.Info,
				},
				ImagePull: clabernetesapisv1alpha1.ConfigImagePull{
					PullThroughOverride: clabernetesconstants.ImagePullThroughModeAuto,
				},
				Naming: clabernetesconstants.NamingModePrefixed,
			},
		}

		managerInstance = m
	})
}

// GetManager returns the config manager -- if the manager has not been initialized it panics.
func GetManager() Manager {
	if managerInstance == nil {
		panic(
			"config manager instance is nil, 'GetManager' should never be called until the " +
				"manager process has been started",
		)
	}

	return managerInstance
}

// Manager is the config manager interface defining the config manager methods.
type Manager interface { //nolint: interfacebloat
	// Start starts the config manager -- this should be called by the clabernetes manager during
	// the "pre-start" phase -- we want this to happen prior to starting controller-runtime manager
	// things. This method attempts to find the clabernetes config configmap and then watches that
	// object. *If* this configmap cannot be found we will log a WARN error but *will not crash*
	// -- we will simply return default (empty) values when the config manager is queried by the
	// controllers.
	Start() error
	// GetGlobalAnnotations returns a map of the "global" annotations from the config -- these are
	// annotations that should be applied to *all* clabernetes objects.
	GetGlobalAnnotations() map[string]string
	// GetGlobalLabels returns a map of the "global" labels from the config -- these are labels
	// that should be applied to *all* clabernetes objects.
	GetGlobalLabels() map[string]string
	// GetAllMetadata returns the global annotations and global labels.
	GetAllMetadata() (map[string]string, map[string]string)
	// GetResourcesForContainerlabKind returns the desired default resources for a containerlab
	// kind/type combo.
	GetResourcesForContainerlabKind(
		containerlabKind string,
		containerlabType string,
	) *k8scorev1.ResourceRequirements
	// GetNodeSelectorsByImage returns the node selectors map for an image.
	GetNodeSelectorsByImage(
		imageName string,
	) map[string]string
	// GetPrivilegedLauncher returns the global config value for the privileged launcher mode.
	GetPrivilegedLauncher() bool
	// GetContainerlabDebug returns the global config value for containerlabDebug.
	GetContainerlabDebug() bool
	// GetContainerlabTimeout returns the global config containerlab timeout value.
	GetContainerlabTimeout() string
	// GetInClusterDNSSuffix returns the in cluster dns suffix as set by the global config.
	GetInClusterDNSSuffix() string
	// GetImagePullThroughMode returns the image pull through mode in the global config.
	GetImagePullThroughMode() string
	// GetImagePullCriSockOverride returns the cri sock path override.
	GetImagePullCriSockOverride() string
	// GetImagePullCriKindOverride returns the cri kind override.
	GetImagePullCriKindOverride() string
	// GetDockerDaemonConfig returns the secret name to mount in /etc/docker -- the secret *must*
	// have a key "daemon.json" so the final mounted file is /etc/docker/daemon.json.
	GetDockerDaemonConfig() string
	// GetDockerConfig returns the secret name to mount in /root/.docker/ -- the secret *must* have
	// a key "config.json" so the final mounted file is /root/.docker/config.json.
	GetDockerConfig() string
	// GetLauncherImage returns the global default launcher image.
	GetLauncherImage() string
	// GetLauncherImagePullPolicy returns the global default launcher image pull policy.
	GetLauncherImagePullPolicy() string
	// GetLauncherLogLevel returns the default launcher log level.
	GetLauncherLogLevel() string
	// GetExtraEnv returns the default extra env vars for setting on launcher containers.
	GetExtraEnv() []k8scorev1.EnvVar
	// GetRemoveTopologyPrefix returns true if the topology prefix should be removed from Topology
	// resources, otherwise false.
	GetRemoveTopologyPrefix() bool
	// GetContainerlabVersion returns the global config containerlab version.
	GetContainerlabVersion() string
}

type manager struct {
	ctx                   context.Context
	logger                claberneteslogging.Instance
	appName               string
	namespace             string
	kubeClabernetesClient *clabernetesgeneratedclientset.Clientset
	lock                  *sync.RWMutex
	started               bool
	lastHash              string
	config                *clabernetesapisv1alpha1.ConfigSpec
}

func (m *manager) Start() error {
	if m.started {
		m.logger.Info("attempting to start already started config manager, this is a no-op")

		return nil
	}

	found := true

	config, err := m.kubeClabernetesClient.C9sV1alpha1().
		Configs(m.namespace).
		Get(m.ctx, clabernetesconstants.Clabernetes, metav1.GetOptions{})
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			m.logger.Warn(
				"did not find clabernetes global config, will continue but no global" +
					" configs will be applied until/unless this config shows up!",
			)

			found = false
		} else {
			m.logger.Criticalf("encountered error fetching global config, err: %s", err)

			return err
		}
	}

	if found {
		// if we found the config we always load it up or at least check if the hash changed and
		// then load it up
		m.load(config)
	}

	m.started = true

	m.logger.Debug("starting config watch go routine and running forever or until sigint...")

	go m.watchConfig()

	return nil
}

func (m *manager) load(config *clabernetesapisv1alpha1.Config) {
	m.logger.Debug("re-loading config contents...")

	dataBytes, err := yaml.Marshal(config.Spec)
	if err != nil {
		m.logger.Warnf("failed marshaling config contents to bytes, error: %s", err)

		return
	}

	newHash := clabernetesutil.HashBytes(dataBytes)

	if m.lastHash == newHash {
		m.logger.Debug("config contents hash matches last recorded hash, nothing to do")

		return
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	newConfig := config.Spec.DeepCopy()

	// filter out any "reserved" labels (we dont use annotations for anything so those can be left
	// alone) -- this means anything starting w/ "appname/" i guess
	for k := range newConfig.Metadata.Labels {
		if strings.HasPrefix(k, fmt.Sprintf("%s/", m.appName)) {
			m.logger.Warnf(
				"ignoring user provided global label '%s' labels starting with '%s/' are reserved",
				k,
				m.appName,
			)

			delete(newConfig.Metadata.Labels, k)
		}
	}

	m.config = newConfig
}

func (m *manager) watchConfig() {
	listOptions := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", clabernetesconstants.Clabernetes),
		Watch:         true,
	}

	watch, err := m.kubeClabernetesClient.C9sV1alpha1().
		Configs(m.namespace).
		Watch(m.ctx, listOptions)
	if err != nil {
		m.logger.Criticalf("failed watching clabernetes config, err: %s", err)
	}

	for event := range watch.ResultChan() {
		switch event.Type {
		case apimachinerywatch.Added, apimachinerywatch.Modified:
			m.logger.Info("processing global config add or modification event")

			configMap, ok := event.Object.(*clabernetesapisv1alpha1.Config)
			if !ok {
				m.logger.Warn("failed casting event object to config, this is probably a bug")

				continue
			}

			m.load(configMap)
		case apimachinerywatch.Deleted:
			m.logger.Warn(
				"global config was *deleted*, will continue with empty config...",
			)

			m.load(&clabernetesapisv1alpha1.Config{})
		case apimachinerywatch.Bookmark, apimachinerywatch.Error:
		}
	}
}
