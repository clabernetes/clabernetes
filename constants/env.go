package constants

const (
	// AppNameEnv is the environment variable name of the "appName" as supplied to helm
	// if not set the default will always be AppNameDefault.
	AppNameEnv = "APP_NAME"

	// ManagerLoggerLevelEnv is the environment variable name that can be used to set the
	// clabernetes manager logger level. This is the logger for the "main" process, not the
	// individual controllers.
	ManagerLoggerLevelEnv = "MANAGER_LOGGER_LEVEL"

	// ControllerLoggerLevelEnv is the environment variable name that can be used to set the
	// clabernetes controllers logger level.
	ControllerLoggerLevelEnv = "CONTROLLER_LOGGER_LEVEL"

	// ClientOperationTimeoutMultiplierEnv is the multiplier applied to the default client
	// operation timeout.
	ClientOperationTimeoutMultiplierEnv = "CLIENT_OPERATION_TIMEOUT_MULTIPLIER"
)

const (
	// HTTPProxyEnv is the standard env var for proxying http traffic. If set on a launcher pod
	// (i.e. via the topology or global config extra env vars) it is passed to the launcher's
	// docker daemon so images can be pulled through the proxy.
	HTTPProxyEnv = "HTTP_PROXY"

	// HTTPProxyEnvLower is the lowercase variant of HTTPProxyEnv.
	HTTPProxyEnvLower = "http_proxy"

	// HTTPSProxyEnv is the standard env var for proxying https traffic, see also HTTPProxyEnv.
	HTTPSProxyEnv = "HTTPS_PROXY"

	// HTTPSProxyEnvLower is the lowercase variant of HTTPSProxyEnv.
	HTTPSProxyEnvLower = "https_proxy"

	// NoProxyEnv is the standard env var holding hosts/cidrs/domains that should *not* be
	// proxied, see also HTTPProxyEnv.
	NoProxyEnv = "NO_PROXY"

	// NoProxyEnvLower is the lowercase variant of NoProxyEnv.
	NoProxyEnvLower = "no_proxy"
)

const (
	// GitHubTokenEnv is the env var that holds (optionally of course) a GitHub token -- this is
	// useful for the clabverter tool as well as the launcher where we *may* need to use the
	// GitHub api to list contents of a directory (this is specifically for dealing with large files
	// that don't fit in configmaps).
	GitHubTokenEnv = "GITHUB_TOKEN" //nolint:gosec
)

const (
	// NodeNameEnv is the env var name that holds the name of the node a pod is on -- this comes
	// from the field object selector in a deployment/pod spec.
	NodeNameEnv = "NODE_NAME"
	// PodNameEnv is the env var name that holds the name of the pod.
	PodNameEnv = "POD_NAME"
	// PodNamespaceEnv is the env var name that holds the namespace of the pod.
	PodNamespaceEnv = "POD_NAMESPACE"
	// ManagerNamespaceEnv is the environment variable that holds the namespace that the manager is
	// running in.
	ManagerNamespaceEnv = "MANAGER_NAMESPACE"
)

const (
	// LauncherLoggerLevelEnv is the environment variable name that can be used to set the
	// clabernetes launcher logger level.
	LauncherLoggerLevelEnv = "LAUNCHER_LOGGER_LEVEL"

	// LauncherContainerlabDebug is the environment variable name that can be used to enable the
	// debug flag of clabernetes when invoked on the launcher pod.
	LauncherContainerlabDebug = "LAUNCHER_CONTAINERLAB_DEBUG"

	// LauncherContainerlabTimeout is the environment variable name that can be used to set the
	// value of the timeout flag of clabernetes when invoked on the launcher pod.
	LauncherContainerlabTimeout = "LAUNCHER_CONTAINERLAB_TIMEOUT"

	// LauncherContainerlabPersist is the environment variable name that can be used to enable the
	// persistence of clabernetes when invoked on the launcher pod.
	LauncherContainerlabPersist = "LAUNCHER_CONTAINERLAB_PERSIST"

	// LauncherImageEnv env var that tells the controllers what image to use for clabernetes
	// (launcher) pods.
	LauncherImageEnv = "LAUNCHER_IMAGE"

	// LauncherPrivilegedEnv is an envar that indicates if the launcher is launched with
	// privileged mode or our "not so privileged mode".
	LauncherPrivilegedEnv = "LAUNCHER_PRIVILEGED"

	// LauncherInsecureRegistries env var that tells the launcher pods which registries are
	// insecure. Should be set by the controller via the topology spec.
	LauncherInsecureRegistries = "LAUNCHER_INSECURE_REGISTRIES"

	// LauncherImagePullThroughModeEnv env var tells the manager how to configure the launcher,
	// which in turn tells the launcher how it should attempt to pull images for the node it
	// represents.
	LauncherImagePullThroughModeEnv = "LAUNCHER_IMAGE_PULL_THROUGH_MODE"

	// LauncherCRIKindEnv env var tells the launcher what CRI sock is mounted in it (if configured).
	LauncherCRIKindEnv = "LAUNCHER_CRI_KIND"

	// LauncherNodeNameEnv is the env var that holds the name of the node in the original topology
	// that a given launcher is responsible for.
	LauncherNodeNameEnv = "LAUNCHER_NODE_NAME"

	// LauncherNodeImageEnv is the env var that holds the image name of the node in the original
	// topology that a given launcher is responsible for.
	LauncherNodeImageEnv = "LAUNCHER_NODE_IMAGE"

	// LauncherContainerlabVersion is the env var that holds the possibly user specified version of
	// containerlab to download and use in the launcher.
	LauncherContainerlabVersion = "LAUNCHER_CONTAINERLAB_VERSION"

	// LauncherTCPProbePort is the env var that holds the port to use in the tcp probe (if
	// configured).
	LauncherTCPProbePort = "LAUNCHER_TCP_PROBE_PORT"

	// LauncherSSHProbePort is the env var that holds the port to use in the ssh probe (if
	// configured).
	LauncherSSHProbePort = "LAUNCHER_SSH_PROBE_PORT"

	// LauncherSSHProbeUsername is the env var that holds the username to use in the ssh probe (if
	// configured).
	LauncherSSHProbeUsername = "LAUNCHER_SSH_PROBE_USERNAME"

	// LauncherSSHProbePassword is the env var that holds the password to use in the ssh probe (if
	// configured).
	LauncherSSHProbePassword = "LAUNCHER_SSH_PROBE_PASSWORD" //nolint:gosec

	// LauncherGroupMembersEnv is the env var that holds the (comma separated, sorted) names of
	// the *other* (containerlab) nodes hosted by this launcher pod -- that is, the nodes grouped
	// onto this launcher via `network-mode: container:<this launcher's node>`.
	LauncherGroupMembersEnv = "LAUNCHER_GROUP_MEMBERS"

	// LauncherMgmtNetworkEnv is the env var that holds the (json encoded) containerlab management
	// network settings the launcher should render into its topology file (if any).
	LauncherMgmtNetworkEnv = "LAUNCHER_MGMT_NETWORK"

	// LauncherPullSecretsEnv is the env var that holds the (comma separated) secret names the
	// launcher may use when pulling images via the cluster CRI.
	LauncherPullSecretsEnv = "LAUNCHER_PULL_SECRETS" //nolint:gosec

	// LauncherInClusterDNSSuffixEnv is the env var that holds the in cluster dns suffix the
	// launcher uses when deriving tunnel destinations (fabric service names).
	LauncherInClusterDNSSuffixEnv = "LAUNCHER_IN_CLUSTER_DNS_SUFFIX"
)

const (
	// ClickerLoggerLevelEnv is the environment variable name that can be used to set the
	// cl(abernetes t)ick(l)er logger level.
	ClickerLoggerLevelEnv = "CLICKER_LOGGER_LEVEL"

	// ClickerWorkerImage is the environment variable name that can be used to set the
	// cl(abernetes t)ick(l)er worker image -- that is, the image that is deployed in a pod on all
	// target nodes, by default this is simply 'busybox'.
	ClickerWorkerImage = "CLICKER_WORKER_IMAGE"

	// ClickerWorkerCommand is the command for the worker -- defaults to "/bin/sh".
	ClickerWorkerCommand = "CLICKER_WORKER_COMMAND"

	// ClickerWorkerScript is the script for the clicker worker -- defaults to 'echo "hello, there"'
	// since we can't know what users will need here.
	ClickerWorkerScript = "CLICKER_WORKER_SCRIPT"

	// ClickerWorkerResources -- see also ClickerGlobalAnnotations -- same thing just for the worker
	// pod resources, we'll just unmarshal to k8scorev1.ResourceRequirements.
	ClickerWorkerResources = "CLICKER_WORKER_RESOURCES"

	// ClickerGlobalAnnotations is the env var where we store the global annotations from the helm
	// deployment -- these annotations need to be stored such that they can be set on the actual
	// "worker" pods as well. In "normal" clabernetes operations this is stored in the configmap
	// where other config things are stored, but in context of the clicker this configmap may not
	// exist, so we'll just stuff these into env vars.
	ClickerGlobalAnnotations = "CLICKER_GLOBAL_ANNOTATIONS"

	// ClickerGlobalLabels -- see also ClickerGlobalAnnotations -- same thing just for labels.
	ClickerGlobalLabels = "CLICKER_GLOBAL_LABELS"
)
