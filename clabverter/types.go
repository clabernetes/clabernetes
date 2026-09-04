package clabverter

type startupConfigConfigMapTemplateVars struct {
	Name          string
	Namespace     string
	StartupConfig string
}

type extraFilesConfigMapTemplateVars struct {
	Name       string
	Namespace  string
	ExtraFiles map[string]string
}

type topologyConfigMapTemplateVars struct {
	NodeName      string
	ConfigMapName string
	FilePath      string
	FileName      string
	FileMode      string
}

type topologyFileFromURLTemplateVars struct {
	URL      string
	FilePath string
}

type containerlabTemplateVars struct {
	Name             string
	Namespace        string
	ClabConfig       string
	Files            map[string][]topologyConfigMapTemplateVars
	FilesFromURL     map[string][]topologyFileFromURLTemplateVars
	ImagePullSecrets []string
	DisableExpose    bool
}

type renderedContent struct {
	friendlyName string
	fileName     string
	content      []byte
}

type sourceDestinationPathPair struct {
	sourcePath      string
	destinationPath string
	// mode is either "read" (default) or "execute"
	// and reflects the mode of the source file
	// referenced by the sourcePath
	mode string
}

type gitHubPathInfo struct {
	Path string `json:"path"`
	Type string `json:"type"`
}
