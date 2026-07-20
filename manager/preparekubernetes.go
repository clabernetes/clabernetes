package manager

import (
	"fmt"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	apimachineryscheme "k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

// registerToScheme registers apis needed by clabernetes to the schema.
func registerToScheme(c clabernetesmanagertypes.Clabernetes) error {
	scheme := c.GetScheme()

	apisToRegisterFuncs := []func() (apimachineryscheme.GroupVersion, []apimachineryruntime.Object){
		clabernetesapisv1alpha1.GetAPIs,
	}

	for _, apiToRegisterFunc := range apisToRegisterFuncs {
		gv, objects := apiToRegisterFunc()

		for _, object := range objects {
			scheme.AddKnownTypes(gv, object)
		}

		metav1.AddToGroupVersion(scheme, gv)
	}

	err := clientgoscheme.AddToScheme(scheme)
	if err != nil {
		return fmt.Errorf("adding clientgo to scheme: %w", err)
	}

	err = apiextensionsv1.AddToScheme(scheme)
	if err != nil {
		return fmt.Errorf("adding apiextensions to scheme: %w", err)
	}

	return nil
}
