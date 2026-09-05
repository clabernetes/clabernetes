package constants

// Version of the clabernetes manager. Set with build flags, so leave at 0.0.0.
var Version = "0.0.0" //nolint: gochecknoglobals

const (
	// Clabernetes is the name... clabernetes.
	Clabernetes = "clabernetes"

	// Clabverter is a constant for the lovely name "clabverter".
	Clabverter = "clabverter"

	// True is a constant representing the string "true".
	True = "true"

	// False is a constant representing the string "false".
	False = "false"

	// Default is a constant for the string default -- often used for keys in clabernetes maps.
	Default = "default"

	// AppNameDefault is the default name for the "app" (the helm value appName) -- "clabernetes".
	AppNameDefault = "clabernetes"

	// ManagementMeshVXLANPort is the UDP port of the kernel VXLAN management mesh joining a
	// namespace's interposed Pods into one management domain.
	ManagementMeshVXLANPort = 14789

	// FabricWireServicePort is the UDP port of the c9s-owned fabric wire carrying cross-Pod
	// direct Link frames, carrier state, and peer heartbeats between connectivity sidecars.
	FabricWireServicePort = 14790

	// ConnectivityReadinessPort is the TCP port on which the connectivity sidecar answers the
	// kubelet's readiness probe on the Pod address; an HTTP probe costs the node nothing, where
	// an exec probe would start the runtime binary every second in every Pod.
	ConnectivityReadinessPort = 14791

	// TCP is... TCP.
	TCP = "TCP"

	// UDP is... UDP.
	UDP = "UDP"

	// FileModeRead is "read". Used for configmap mount permissions in the
	// TopologySpec/FilesFromConfigMap.
	FileModeRead = "read"

	// FileModeExecute is "execute". Used for configmap mount permissions in the
	// TopologySpec/FilesFromConfigMap.
	FileModeExecute = "execute"

	// HostKeyword is the containerlab reserved keyword to define host links endpoints.
	HostKeyword = "host"
)
