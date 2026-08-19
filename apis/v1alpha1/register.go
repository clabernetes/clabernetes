package v1alpha1

import (
	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachineryscheme "k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// Version is the API version.
	Version = "v1alpha1"
)

var (
	schemeBuilder      = apimachineryruntime.NewSchemeBuilder(addKnownTypes)
	localSchemeBuilder = &schemeBuilder
)

// SchemeGroupVersion is group version used to register these objects.
var SchemeGroupVersion = apimachineryscheme.GroupVersion{
	Group:   clabernetesapis.Group,
	Version: Version,
}

// AddToScheme adds the types in this group-version to the given scheme.
var AddToScheme = localSchemeBuilder.AddToScheme

func addKnownTypes(scheme *apimachineryruntime.Scheme) error {
	_, objects := GetAPIs()

	for _, object := range objects {
		scheme.AddKnownTypes(SchemeGroupVersion, object)
	}

	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)

	return nil
}

// GetAPIs returns the information necessary to register this package's types to a scheme.
func GetAPIs() (apimachineryscheme.GroupVersion, []apimachineryruntime.Object) {
	return SchemeGroupVersion, []apimachineryruntime.Object{
		&Config{},
		&ConfigList{},
		&Link{},
		&LinkList{},
		&Node{},
		&NodeList{},
		&LauncherProfile{},
		&LauncherProfileList{},
		&Topology{},
		&TopologyList{},
	}
}
