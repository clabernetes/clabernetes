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

	// DeviceRuntimeImageEnv is the manager image used for isolated planning and thin direct-Pod
	// helpers. It contains the c9s integration binary, not containerlab kind knowledge.
	DeviceRuntimeImageEnv = "DEVICE_RUNTIME_IMAGE"

	// ClientOperationTimeoutMultiplierEnv is the multiplier applied to the default client
	// operation timeout.
	ClientOperationTimeoutMultiplierEnv = "CLIENT_OPERATION_TIMEOUT_MULTIPLIER"
)

const (
	// GitHubTokenEnv is the env var that holds (optionally of course) a GitHub token -- this is
	// useful for the clabverter tool where we *may* need to use the GitHub api to list contents
	// of a directory (this is specifically for dealing with large files that don't fit in
	// configmaps).
	GitHubTokenEnv = "GITHUB_TOKEN" //nolint:gosec
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
