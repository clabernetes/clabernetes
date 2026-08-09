package node

import (
	"fmt"
	"maps"
	"net"
	"reflect"
	"strings"

	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/srl-labs/clabernetes/config"
	clabernetesconstants "github.com/srl-labs/clabernetes/constants"
	claberneteslogging "github.com/srl-labs/clabernetes/logging"
	clabernetesutilkubernetes "github.com/srl-labs/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	exposeTypeNone     = "None"
	exposeTypeHeadless = "Headless"
)

// FabricServiceName returns the name of the fabric (inter launcher connectivity) service of the
// given (containerlab) node -- tunnels to the node are pointed at
// `<name>.<namespace>.<dns-suffix>` by the remote launchers.
func FabricServiceName(nodeName string) string {
	return fmt.Sprintf("%s-vx", nodeName)
}

func exposeTypeToServiceType(exposeType string) k8scorev1.ServiceType {
	switch exposeType {
	case string(k8scorev1.ServiceTypeClusterIP), exposeTypeHeadless:
		return k8scorev1.ServiceTypeClusterIP
	default:
		return k8scorev1.ServiceTypeLoadBalancer
	}
}

// ServiceReconciler renders/validates the fabric and expose services for Nodes -- exposed for
// testing purposes.
type ServiceReconciler struct {
	log                 claberneteslogging.Instance
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewServiceReconciler returns an instance of ServiceReconciler.
func NewServiceReconciler(
	log claberneteslogging.Instance,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *ServiceReconciler {
	return &ServiceReconciler{
		log:                 log,
		configManagerGetter: configManagerGetter,
	}
}

// launcherSelectorLabels returns the labels selecting the launcher pod of the given launcher
// node -- services of grouped (secondary) nodes select their primary's pod.
func launcherSelectorLabels(launcherNode string) map[string]string {
	return map[string]string{
		clabernetesconstants.LabelApp:          clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:         launcherNode,
		clabernetesconstants.LabelTopologyNode: launcherNode,
	}
}

// RenderFabricService renders the fabric (vxlan/slurpeeth) service for the given node -- every
// node gets one, and for grouped nodes the service selects the pod of the group's launcher
// (primary) node. This per-node service is what lets launchers derive tunnel destinations from
// a link spec alone.
func (r *ServiceReconciler) RenderFabricService(
	node *clabernetesapisv1alpha1.Node,
	launcherNode string,
) *k8scorev1.Service {
	service := r.renderServiceBase(
		node,
		FabricServiceName(node.GetName()),
		launcherNode,
		clabernetesconstants.TopologyServiceTypeFabric,
	)

	service.Spec.Type = k8scorev1.ServiceTypeClusterIP
	service.Spec.Ports = []k8scorev1.ServicePort{
		{
			Name:     string(clabernetesapisv1alpha1.LinkConnectivityVXLAN),
			Protocol: clabernetesconstants.UDP,
			Port:     clabernetesconstants.VXLANServicePort,
			TargetPort: intstr.IntOrString{
				IntVal: clabernetesconstants.VXLANServicePort,
			},
		},
		{
			Name:     string(clabernetesapisv1alpha1.LinkConnectivitySlurpeeth),
			Protocol: clabernetesconstants.TCP,
			Port:     clabernetesconstants.SlurpeethServicePort,
			TargetPort: intstr.IntOrString{
				IntVal: clabernetesconstants.SlurpeethServicePort,
			},
		},
	}

	return service
}

// RenderExposeService renders the expose service for the given node from the *allocations* in
// exposedPorts (see ResolveExposedPorts) -- allocations are made into the node status first and
// the service is programmed from them. Returns nil if the node exposes nothing (no ports, or
// expose disabled/None per the node's resolved profile).
func (r *ServiceReconciler) RenderExposeService(
	node *clabernetesapisv1alpha1.Node,
	launcherNode string,
	resolvedProfile *ResolvedProfile,
	exposedPorts *clabernetesapisv1alpha1.NodeExposedPorts,
) *k8scorev1.Service {
	if exposedPorts == nil || len(exposedPorts.Ports) == 0 ||
		resolvedProfile.DisableExpose || resolvedProfile.ExposeType == exposeTypeNone {
		return nil
	}

	service := r.renderServiceBase(
		node,
		node.GetName(),
		launcherNode,
		clabernetesconstants.TopologyServiceTypeExpose,
	)

	service.Spec.Type = exposeTypeToServiceType(resolvedProfile.ExposeType)

	if resolvedProfile.ExposeType == exposeTypeHeadless {
		service.Spec.ClusterIP = k8scorev1.ClusterIPNone
	}

	r.renderExposeServiceLoadBalancerIP(service, node, resolvedProfile)

	ports := make([]k8scorev1.ServicePort, len(exposedPorts.Ports))

	for idx, port := range exposedPorts.Ports {
		ports[idx] = k8scorev1.ServicePort{
			Name: fmt.Sprintf(
				"port-%d-%s", port.DestinationPort, strings.ToLower(port.Protocol),
			),
			Protocol: k8scorev1.Protocol(port.Protocol),
			Port:     int32(port.DestinationPort), //nolint:gosec
			TargetPort: intstr.IntOrString{
				IntVal: int32(port.ExposePort), //nolint:gosec
			},
		}
	}

	service.Spec.Ports = ports

	return service
}

// Conforms asserts if a given service conforms with a rendered service -- this isn't checking
// if the services are exactly the same, just checking that the parts clabernetes cares about
// are the same.
func (r *ServiceReconciler) Conforms( //nolint:gocyclo
	existingService,
	renderedService *k8scorev1.Service,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	if !reflect.DeepEqual(existingService.Spec.Selector, renderedService.Spec.Selector) {
		return false
	}

	if existingService.Spec.Type != renderedService.Spec.Type {
		return false
	}

	if serviceIsHeadless(existingService) != serviceIsHeadless(renderedService) {
		return false
	}

	if existingService.Spec.LoadBalancerIP != renderedService.Spec.LoadBalancerIP {
		return false
	}

	if len(renderedService.Spec.Ports) != len(existingService.Spec.Ports) {
		return false
	}

	for _, expectedPort := range renderedService.Spec.Ports {
		var expectedPortExists bool

		for _, actualPort := range existingService.Spec.Ports {
			if expectedPort.Name != actualPort.Name {
				continue
			}

			if expectedPort.Port != actualPort.Port {
				break
			}

			if expectedPort.Protocol != actualPort.Protocol {
				break
			}

			if !reflect.DeepEqual(expectedPort.TargetPort, actualPort.TargetPort) {
				break
			}

			expectedPortExists = true
		}

		if !expectedPortExists {
			// port doesnt exist or is wrong
			return false
		}
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingService.ObjectMeta.Annotations,
		renderedService.ObjectMeta.Annotations,
	) {
		return false
	}

	if !clabernetesutilkubernetes.ExistingMapStringStringContainsAllExpectedKeyValues(
		existingService.ObjectMeta.Labels,
		renderedService.ObjectMeta.Labels,
	) {
		return false
	}

	if len(existingService.ObjectMeta.OwnerReferences) != 1 {
		// we should have only one owner reference, the owning node
		return false
	}

	if existingService.ObjectMeta.OwnerReferences[0].UID != expectedOwnerUID {
		// owner ref uid is not us
		return false
	}

	return true
}

// serviceNeedsRecreate returns true for the ClusterIP allocation-mode transition Kubernetes does
// not allow through an update. Both ordinary and headless Services have type ClusterIP, so this
// must be checked separately from Spec.Type.
func serviceNeedsRecreate(existingService, renderedService *k8scorev1.Service) bool {
	return serviceIsHeadless(existingService) != serviceIsHeadless(renderedService)
}

func serviceIsHeadless(service *k8scorev1.Service) bool {
	return service.Spec.ClusterIP == k8scorev1.ClusterIPNone
}

// prepareServiceForUpdate carries API-server allocations and immutable/defaulted networking
// fields into a freshly rendered Service before it is sent through Update.
func prepareServiceForUpdate(existingService, renderedService *k8scorev1.Service) {
	renderedService.SetResourceVersion(existingService.GetResourceVersion())
	renderedService.Spec.ClusterIP = existingService.Spec.ClusterIP
	renderedService.Spec.ClusterIPs = append([]string(nil), existingService.Spec.ClusterIPs...)
	renderedService.Spec.IPFamilies = append(
		[]k8scorev1.IPFamily(nil),
		existingService.Spec.IPFamilies...,
	)

	if existingService.Spec.IPFamilyPolicy != nil {
		policy := *existingService.Spec.IPFamilyPolicy
		renderedService.Spec.IPFamilyPolicy = &policy
	}

	preserveNodePorts := (renderedService.Spec.Type == k8scorev1.ServiceTypeLoadBalancer ||
		renderedService.Spec.Type == k8scorev1.ServiceTypeNodePort) &&
		(existingService.Spec.Type == k8scorev1.ServiceTypeLoadBalancer ||
			existingService.Spec.Type == k8scorev1.ServiceTypeNodePort)

	for renderedPortIdx := range renderedService.Spec.Ports {
		renderedPort := &renderedService.Spec.Ports[renderedPortIdx]

		for existingPortIdx := range existingService.Spec.Ports {
			existingPort := &existingService.Spec.Ports[existingPortIdx]
			if renderedPort.Name != existingPort.Name ||
				renderedPort.Protocol != existingPort.Protocol {
				continue
			}

			if preserveNodePorts && renderedPort.NodePort == 0 {
				renderedPort.NodePort = existingPort.NodePort
			}

			break
		}
	}

	if renderedService.Spec.Type != k8scorev1.ServiceTypeLoadBalancer ||
		existingService.Spec.Type != k8scorev1.ServiceTypeLoadBalancer {
		return
	}

	renderedService.Spec.HealthCheckNodePort = existingService.Spec.HealthCheckNodePort

	if existingService.Spec.AllocateLoadBalancerNodePorts != nil {
		allocateNodePorts := *existingService.Spec.AllocateLoadBalancerNodePorts
		renderedService.Spec.AllocateLoadBalancerNodePorts = &allocateNodePorts
	}

	if existingService.Spec.LoadBalancerClass != nil {
		loadBalancerClass := *existingService.Spec.LoadBalancerClass
		renderedService.Spec.LoadBalancerClass = &loadBalancerClass
	}
}

func (r *ServiceReconciler) renderServiceBase(
	node *clabernetesapisv1alpha1.Node,
	name,
	launcherNode,
	serviceType string,
) *k8scorev1.Service {
	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	labels := map[string]string{
		clabernetesconstants.LabelApp:                 clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:                name,
		clabernetesconstants.LabelTopologyNode:        node.GetName(),
		clabernetesconstants.LabelTopologyServiceType: serviceType,
	}

	maps.Copy(labels, globalLabels)

	if owner, ok := node.GetLabels()[clabernetesconstants.LabelTopologyOwner]; ok {
		labels[clabernetesconstants.LabelTopologyOwner] = owner
	}

	return &k8scorev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   node.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: k8scorev1.ServiceSpec{
			Selector: launcherSelectorLabels(launcherNode),
		},
	}
}

func (r *ServiceReconciler) renderExposeServiceLoadBalancerIP(
	service *k8scorev1.Service,
	node *clabernetesapisv1alpha1.Node,
	resolvedProfile *ResolvedProfile,
) {
	if service.Spec.Type != k8scorev1.ServiceTypeLoadBalancer {
		return
	}

	var raw string

	switch {
	case resolvedProfile.UseNodeMgmtIpv4Address:
		raw = node.Spec.MgmtIPv4
	case resolvedProfile.UseNodeMgmtIpv6Address:
		raw = node.Spec.MgmtIPv6
	}

	if raw == "" {
		return
	}

	ip := net.ParseIP(raw)
	if ip == nil {
		r.log.Warnf(
			"failed to parse mgmt address %q for node %q: invalid IP;"+
				" using auto-assigned LoadBalancerIP",
			raw,
			node.GetName(),
		)

		return
	}

	service.Spec.LoadBalancerIP = ip.String()
}
