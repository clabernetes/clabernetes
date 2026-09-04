package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Config is an object that holds global clabernetes config information. Note that this CR is
// expected to effectively be a global singleton -- that is, there should be only *one* of these,
// and it *must* be named `clabernetes` -- CRD metadata spec will enforce this (via x-validation
// rules).
// +k8s:openapi-gen=true
// +kubebuilder:validation:XValidation:rule=(self.metadata.name == 'clabernetes')
type Config struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigSpec   `json:"spec,omitempty"`
	Status ConfigStatus `json:"status,omitempty"`
}

// ConfigSpec is the spec for a Config resource.
type ConfigSpec struct {
	// Metadata holds "global" metadata -- that is, metadata that is applied to all objects created
	// by the clabernetes controller.
	// +optional
	Metadata ConfigMetadata `json:"metadata"`
	// ImagePull holds direct Pod pull defaults and controller-only OCI metadata trust policy.
	// +optional
	ImagePull ConfigImagePull `json:"imagePull"`
	// Deployment holds generic direct device workload defaults.
	// +optional
	Deployment ConfigDeployment `json:"deployment"`
}

// ConfigStatus is the status for a Config resource.
type ConfigStatus struct{}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConfigList is a list of Config objects.
type ConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Config `json:"items"`
}
