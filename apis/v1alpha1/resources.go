package v1alpha1

import (
	"encoding/json"
	"fmt"

	k8scorev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
)

// ResourceList describes CPU, memory, and additional Kubernetes resources for one container.
// Additional resource names are flattened beside cpu and memory when serialized.
// +kubebuilder:pruning:PreserveUnknownFields
type ResourceList struct {
	// CPU requests or limits the container's CPU resources in Kubernetes quantity format, such as
	// 100m, 500m, "1", or "2.5".
	// +optional
	CPU *apiresource.Quantity `json:"cpu,omitempty"`
	// Memory requests or limits the container's memory in Kubernetes quantity format, such as
	// 512Mi, 2Gi, or 500M.
	// +optional
	Memory *apiresource.Quantity `json:"memory,omitempty"`
	// AdditionalResources holds other Kubernetes resources, such as ephemeral-storage or
	// hugepages-2Mi. They marshal as top-level keys beside cpu and memory, matching core
	// Kubernetes ResourceList syntax rather than an additionalResources wrapper.
	// +optional
	AdditionalResources map[k8scorev1.ResourceName]apiresource.Quantity `json:"-"`
}

// ResourceRequirements describes the compute resources required by a container.
type ResourceRequirements struct {
	// Requests describes the minimum compute resources required. When omitted for a container,
	// Limits is used if explicitly specified; otherwise an implementation-defined value applies.
	// Requests cannot exceed Limits.
	// +optional
	Requests *ResourceList `json:"requests,omitempty"`
	// Limits describes the maximum compute resources allowed. Limits cannot be exceeded.
	// +optional
	Limits *ResourceList `json:"limits,omitempty"`
	// Claims lists names of resources, defined in spec.resourceClaims, that are used by this
	// container. This field depends on the DynamicResourceAllocation feature gate.
	// +listType=map
	// +listMapKey=name
	// +optional
	Claims []k8scorev1.ResourceClaim `json:"claims,omitempty"`
}

// UnmarshalJSON implements the flattened core Kubernetes ResourceList syntax.
func (r *ResourceList) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*r = ResourceList{}

		return nil
	}

	var known struct {
		CPU    *apiresource.Quantity `json:"cpu,omitempty"`
		Memory *apiresource.Quantity `json:"memory,omitempty"`
	}
	if err := json.Unmarshal(data, &known); err != nil {
		return err
	}

	var allValues map[string]json.RawMessage
	if err := json.Unmarshal(data, &allValues); err != nil {
		return err
	}

	*r = ResourceList{CPU: known.CPU, Memory: known.Memory}
	for name, value := range allValues {
		if name == "cpu" || name == "memory" {
			continue
		}

		quantity := apiresource.Quantity{}
		if err := json.Unmarshal(value, &quantity); err != nil {
			return fmt.Errorf("resource %q: %w", name, err)
		}

		if r.AdditionalResources == nil {
			r.AdditionalResources = map[k8scorev1.ResourceName]apiresource.Quantity{}
		}
		r.AdditionalResources[k8scorev1.ResourceName(name)] = quantity
	}

	return nil
}

// MarshalJSON implements the flattened core Kubernetes ResourceList syntax.
func (r *ResourceList) MarshalJSON() ([]byte, error) {
	values := map[string]any{}
	if r.CPU != nil {
		values["cpu"] = r.CPU
	}
	if r.Memory != nil {
		values["memory"] = r.Memory
	}
	for name, quantity := range r.AdditionalResources {
		values[string(name)] = quantity
	}

	return json.Marshal(values)
}

// ToCoreV1ResourceList converts a c9s CPU/memory-aware list into the native Kubernetes map type.
func (r *ResourceList) ToCoreV1ResourceList() k8scorev1.ResourceList {
	if r == nil {
		return nil
	}

	out := k8scorev1.ResourceList{}
	if r.CPU != nil {
		out[k8scorev1.ResourceCPU] = r.CPU.DeepCopy()
	}
	if r.Memory != nil {
		out[k8scorev1.ResourceMemory] = r.Memory.DeepCopy()
	}
	for name, quantity := range r.AdditionalResources {
		out[name] = quantity.DeepCopy()
	}

	return out
}

// ToCoreV1 converts a c9s resource requirement into the native Kubernetes type without sharing
// mutable maps or quantities.
func (r *ResourceRequirements) ToCoreV1() *k8scorev1.ResourceRequirements {
	if r == nil {
		return nil
	}

	return &k8scorev1.ResourceRequirements{
		Requests: r.Requests.ToCoreV1ResourceList(),
		Limits:   r.Limits.ToCoreV1ResourceList(),
		Claims:   append([]k8scorev1.ResourceClaim(nil), r.Claims...),
	}
}

// FromCoreV1ResourceRequirements converts a native Kubernetes resource requirement into the c9s
// schema-aware form used by Config, NodeProfile, and Topology APIs.
func FromCoreV1ResourceRequirements(
	resources *k8scorev1.ResourceRequirements,
) *ResourceRequirements {
	if resources == nil {
		return nil
	}

	return &ResourceRequirements{
		Requests: fromCoreV1ResourceList(resources.Requests),
		Limits:   fromCoreV1ResourceList(resources.Limits),
		Claims:   append([]k8scorev1.ResourceClaim(nil), resources.Claims...),
	}
}

func fromCoreV1ResourceList(resources k8scorev1.ResourceList) *ResourceList {
	if resources == nil {
		return nil
	}

	out := &ResourceList{}
	for name, quantity := range resources {
		switch name {
		case k8scorev1.ResourceCPU:
			cpu := quantity.DeepCopy()
			out.CPU = &cpu
		case k8scorev1.ResourceMemory:
			memory := quantity.DeepCopy()
			out.Memory = &memory
		default:
			if out.AdditionalResources == nil {
				out.AdditionalResources = map[k8scorev1.ResourceName]apiresource.Quantity{}
			}
			out.AdditionalResources[name] = quantity.DeepCopy()
		}
	}

	return out
}
