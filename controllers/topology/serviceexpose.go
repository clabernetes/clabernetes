package topology

import (
	"fmt"
	"maps"
	"net"
	"sort"
	"strings"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	exposeTypeNone     = "None"
	exposeTypeHeadless = "Headless"
)

func exposeTypeToServiceType(exposeType string) k8scorev1.ServiceType {
	switch exposeType {
	case string(k8scorev1.ServiceTypeClusterIP), exposeTypeHeadless:
		return k8scorev1.ServiceTypeClusterIP
	default:
		return k8scorev1.ServiceTypeLoadBalancer
	}
}

// ServiceExposeReconciler is a subcomponent of the "TopologyReconciler" but is exposed for testing
// purposes. This is the component responsible for rendering/validating the "expose" service for a
// clabernetes topology resource.
type ServiceExposeReconciler struct {
	log                 claberneteslogging.Instance
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewServiceExposeReconciler returns an instance of ServiceExposeReconciler.
func NewServiceExposeReconciler(
	log claberneteslogging.Instance,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *ServiceExposeReconciler {
	return &ServiceExposeReconciler{
		log:                 log,
		configManagerGetter: configManagerGetter,
	}
}

// Resolve accepts a mapping of clabernetes configs and a list of services that are -- by owner
// reference and/or labels -- associated with the topology. It returns a ObjectDiffer object
// that contains the missing, extra, and current services for the topology.
func (r *ServiceExposeReconciler) Resolve(
	ownedServices *k8scorev1.ServiceList,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
	owningTopology *clabernetesapisv1alpha1.Topology,
) (*clabernetesutil.ObjectDiffer[*k8scorev1.Service], error) {
	services := &clabernetesutil.ObjectDiffer[*k8scorev1.Service]{
		Current: map[string]*k8scorev1.Service{},
	}

	for i := range ownedServices.Items {
		labels := ownedServices.Items[i].Labels

		if labels == nil {
			return nil, fmt.Errorf(
				"%w: labels are nil, but we expect to see topology owner label here",
				claberneteserrors.ErrInvalidData,
			)
		}

		topologyServiceType := labels[clabernetesconstants.LabelTopologyServiceType]

		if topologyServiceType != clabernetesconstants.TopologyServiceTypeExpose {
			// not the kind of service we're looking for here, we only care about the services
			// used for exposing nodes here.
			continue
		}

		nodeName, ok := labels[clabernetesconstants.LabelTopologyNode]
		if !ok || nodeName == "" {
			return nil, fmt.Errorf(
				"%w: topology node label is missing or empty",
				claberneteserrors.ErrInvalidData,
			)
		}

		services.Current[nodeName] = &ownedServices.Items[i]
	}

	exposedNodes := make([]string, 0)

	disableExpose := owningTopology.Spec.Expose.DisableExpose
	disableAutoExpose := owningTopology.Spec.Expose.DisableAutoExpose

	for nodeName, nodeData := range clabernetesConfigs {
		// disable expose is set to true for the whole spec, nothing should be exposed, so skip
		// every node
		if disableExpose {
			continue
		}

		// if disable auto expose is true *and* there are no ports defined for the node *and*
		// there are no default ports defined for the topology we can skip the node from an expose
		// perspective.
		if disableAutoExpose &&
			len(nodeData.Topology.Nodes[nodeName].Ports) == 0 &&
			len(nodeData.Topology.Defaults.Ports) == 0 {
			continue
		}

		if owningTopology.Spec.Expose.ExposeType == exposeTypeNone {
			// expose type is none -- this means we "expose" the nodes but dont create any
			// service(s) for them (so folks can tickle the pods directly only)
			continue
		}

		exposedNodes = append(exposedNodes, nodeName)
	}

	services.SetMissing(exposedNodes)
	services.SetExtra(exposedNodes)

	return services, nil
}

// Render accepts the owning topology a mapping of clabernetes sub-topology configs and a node name
// and renders the final expose service for this node.
func (r *ServiceExposeReconciler) Render(
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
	nodeName string,
) *k8scorev1.Service {
	if owningTopology.Spec.Expose.ExposeType == exposeTypeNone {
		return nil
	}

	owningTopologyName := owningTopology.GetName()

	serviceName := fmt.Sprintf("%s-%s", owningTopologyName, nodeName)

	if ResolveTopologyRemovePrefix(owningTopology) {
		serviceName = nodeName
	}

	service := r.renderServiceBase(
		owningTopology,
		serviceName,
		nodeName,
	)

	r.processMgmtLoadbalanacerExpose(
		owningTopology,
		reconcileData, service, nodeName)

	r.renderServicePorts(
		reconcileData,
		service,
		nodeName,
	)

	return service
}

// RenderAll accepts the owning topology a mapping of clabernetes sub-topology configs and a
// list of node names and renders the final expose services for the given nodes.
func (r *ServiceExposeReconciler) RenderAll(
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
	nodeNames []string,
) []*k8scorev1.Service {
	services := make([]*k8scorev1.Service, len(nodeNames))

	if owningTopology.Spec.Expose.ExposeType == exposeTypeNone {
		return services
	}

	for idx, nodeName := range nodeNames {
		services[idx] = r.Render(
			owningTopology,
			reconcileData,
			nodeName,
		)
	}

	return services
}

// Conforms checks if the existingService conforms with the renderedService.
func (r *ServiceExposeReconciler) Conforms(
	existingService,
	renderedService *k8scorev1.Service,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	return ServiceConforms(existingService, renderedService, expectedOwnerUID)
}

func (r *ServiceExposeReconciler) renderServiceBase(
	owningTopology *clabernetesapisv1alpha1.Topology,
	name,
	nodeName string,
) *k8scorev1.Service {
	owningTopologyName := owningTopology.GetName()

	annotations, globalLabels := r.configManagerGetter().GetAllMetadata()

	deploymentName := fmt.Sprintf("%s-%s", owningTopologyName, nodeName)

	if ResolveTopologyRemovePrefix(owningTopology) {
		deploymentName = nodeName
	}

	selectorLabels := map[string]string{
		clabernetesconstants.LabelApp:           clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName:          deploymentName,
		clabernetesconstants.LabelTopologyOwner: owningTopologyName,
		clabernetesconstants.LabelTopologyNode:  nodeName,
	}

	labels := map[string]string{
		clabernetesconstants.LabelTopologyKind:        GetTopologyKind(owningTopology),
		clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeExpose, //nolint:lll

	}

	maps.Copy(labels, selectorLabels)
	maps.Copy(labels, globalLabels)

	serviceSpec := k8scorev1.ServiceSpec{
		Selector: selectorLabels,
		// if we ever get here we know expose is not none, so we can just cast the string from
		// our crd to the appropriate flavor service
		Type: exposeTypeToServiceType(owningTopology.Spec.Expose.ExposeType),
	}

	if owningTopology.Spec.Expose.ExposeType == exposeTypeHeadless {
		serviceSpec.ClusterIP = k8scorev1.ClusterIPNone
	}

	return &k8scorev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   owningTopology.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: serviceSpec,
	}
}

func (r *ServiceExposeReconciler) parseContainerlabTopologyPortsSection(
	portDefinition string,
) (bool, *k8scorev1.ServicePort) {
	typedPort, err := clabernetesutilcontainerlab.ProcessPortDefinition(portDefinition)
	if err != nil {
		r.log.Warnf("skipping port due to the following error: %s", err)

		return true, nil
	}

	return false, &k8scorev1.ServicePort{
		Name: fmt.Sprintf(
			"port-%d-%s", typedPort.DestinationPort, strings.ToLower(typedPort.Protocol),
		),
		Protocol: k8scorev1.Protocol(typedPort.Protocol),
		Port:     int32(typedPort.DestinationPort), //nolint: gosec
		TargetPort: intstr.IntOrString{
			IntVal: int32(typedPort.ExposePort), //nolint: gosec
		},
	}
}

func (r *ServiceExposeReconciler) renderServicePorts(
	reconcileData *ReconcileData,
	service *k8scorev1.Service,
	nodeName string,
) {
	reconcileData.ResolvedExposedPorts[nodeName] = &clabernetesapisv1alpha1.ExposedPorts{
		TCPPorts: make([]int, 0),
		UDPPorts: make([]int, 0),
	}

	ports := make([]k8scorev1.ServicePort, 0)

	// for actual containerlab configs we copy the users given defaults into each "sub topology" --
	// so in the case of containerlab we want to make sure we also iterate over the "default" or
	// topology wide ports that were specified. in this process we dont want to duplicate things, so
	// we use a simple set implementation to make sure we aren't doubling up on any port
	// definitions.
	allContainerlabPorts := clabernetesutil.NewStringSet()

	allContainerlabPorts.Extend(
		reconcileData.ResolvedConfigs[nodeName].Topology.Nodes[nodeName].Ports,
	)

	allContainerlabPorts.Extend(reconcileData.ResolvedConfigs[nodeName].Topology.Defaults.Ports)

	allContainerlabPortsItems := allContainerlabPorts.Items()
	sort.Strings(allContainerlabPortsItems)

	for _, portDefinition := range allContainerlabPortsItems {
		shouldSkip, port := r.parseContainerlabTopologyPortsSection(portDefinition)

		if shouldSkip {
			continue
		}

		ports = append(ports, *port)

		// dont forget to update the exposed ports status bits
		if port.Protocol == clabernetesconstants.TCP {
			reconcileData.ResolvedExposedPorts[nodeName].TCPPorts = append(
				reconcileData.ResolvedExposedPorts[nodeName].TCPPorts,
				int(port.Port),
			)
		} else {
			reconcileData.ResolvedExposedPorts[nodeName].UDPPorts = append(
				reconcileData.ResolvedExposedPorts[nodeName].UDPPorts,
				int(port.Port),
			)
		}
	}

	service.Spec.Ports = ports
}

func (r *ServiceExposeReconciler) processMgmtLoadbalanacerExpose(
	owningTopology *clabernetesapisv1alpha1.Topology,
	reconcileData *ReconcileData,
	service *k8scorev1.Service,
	nodeName string,
) {
	var mgmtProtocol string

	if owningTopology.Spec.Expose.UseNodeMgmtIpv4Address {
		mgmtProtocol = "ipv4"
	} else if owningTopology.Spec.Expose.UseNodeMgmtIpv6Address {
		mgmtProtocol = "ipv6"
	}

	if mgmtProtocol == "" {
		// nothin to do cuz we dunno v4 vs v6
		return
	}

	if service.Spec.Type != k8scorev1.ServiceTypeLoadBalancer {
		// also nothing to do, not a lb
		return
	}

	// If we get here pull mgmt-ip (v4 or v6) from the topology and assign as LoadBalancerIP.
	// IPv4 takes precedence if both are set.
	cfg, ok := reconcileData.ResolvedConfigs[nodeName]

	if ok {
		// pull raw string from the right field
		var raw string

		switch mgmtProtocol {
		case "ipv4":
			raw = cfg.Topology.Nodes[nodeName].MgmtIPv4
		case "ipv6":
			raw = cfg.Topology.Nodes[nodeName].MgmtIPv6
		}

		if raw != "" {
			// validate (works for both v4 & v6)
			ip := net.ParseIP(raw)

			if ip == nil {
				r.log.Warnf(
					"failed to parse mgmt-%s %q for node %q: invalid IP;"+
						" using auto-assigned LoadBalancerIP",
					mgmtProtocol,
					raw,
					nodeName,
				)
			} else {
				service.Spec.LoadBalancerIP = ip.String()
			}
		}
	}
}
