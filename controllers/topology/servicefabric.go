package topology

import (
	"fmt"
	"maps"

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

// ServiceFabricReconciler is a subcomponent of the "TopologyReconciler" but is exposed for testing
// purposes. This is the component responsible for rendering/validating the "fabric" service for a
// clabernetes topology resource.
type ServiceFabricReconciler struct {
	log                 claberneteslogging.Instance
	configManagerGetter clabernetesconfig.ManagerGetterFunc
}

// NewServiceFabricReconciler returns an instance of ServiceFabricReconciler.
func NewServiceFabricReconciler(
	log claberneteslogging.Instance,
	configManagerGetter clabernetesconfig.ManagerGetterFunc,
) *ServiceFabricReconciler {
	return &ServiceFabricReconciler{
		log:                 log,
		configManagerGetter: configManagerGetter,
	}
}

// Resolve accepts a mapping of clabernetes configs and a list of services that are -- by owner
// reference and/or labels -- associated with the topology. It returns a ObjectDiffer object
// that contains the missing, extra, and current services for the topology.
func (r *ServiceFabricReconciler) Resolve(
	ownedServices *k8scorev1.ServiceList,
	clabernetesConfigs map[string]*clabernetesutilcontainerlab.Config,
	_ *clabernetesapisv1alpha1.Topology,
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

		if topologyServiceType != clabernetesconstants.TopologyServiceTypeFabric {
			// not the kind of service we're looking for here, we only care about the services
			// used for connecting the nodes together here.
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

	allNodes := make([]string, len(clabernetesConfigs))

	var nodeIdx int

	for nodeName := range clabernetesConfigs {
		allNodes[nodeIdx] = nodeName

		nodeIdx++
	}

	services.SetMissing(allNodes)
	services.SetExtra(allNodes)

	return services, nil
}

// Render accepts the owning topology a mapping of clabernetes sub-topology configs and a node name
// and renders the final fabric service for this node.
func (r *ServiceFabricReconciler) Render(
	owningTopology *clabernetesapisv1alpha1.Topology,
	nodeName string,
) *k8scorev1.Service {
	owningTopologyName := owningTopology.GetName()

	serviceName := fmt.Sprintf("%s-%s-vx", owningTopologyName, nodeName)

	if ResolveTopologyRemovePrefix(owningTopology) {
		serviceName = fmt.Sprintf("%s-vx", nodeName)
	}

	service := r.renderServiceBase(
		owningTopology,
		serviceName,
		nodeName,
	)

	return service
}

// RenderAll accepts the owning topology a mapping of clabernetes sub-topology configs and a
// list of node names and renders the final fabric services for the given nodes.
func (r *ServiceFabricReconciler) RenderAll(
	owningTopology *clabernetesapisv1alpha1.Topology,
	nodeNames []string,
) []*k8scorev1.Service {
	services := make([]*k8scorev1.Service, len(nodeNames))

	for idx, nodeName := range nodeNames {
		services[idx] = r.Render(
			owningTopology,
			nodeName,
		)
	}

	return services
}

// Conforms checks if the existingService conforms with the renderedService.
func (r *ServiceFabricReconciler) Conforms(
	existingService,
	renderedService *k8scorev1.Service,
	expectedOwnerUID apimachinerytypes.UID,
) bool {
	return ServiceConforms(existingService, renderedService, expectedOwnerUID)
}

func (r *ServiceFabricReconciler) renderServiceBase(
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
		clabernetesconstants.LabelTopologyServiceType: clabernetesconstants.TopologyServiceTypeFabric, //nolint:lll

	}

	maps.Copy(labels, selectorLabels)
	maps.Copy(labels, globalLabels)

	return &k8scorev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   owningTopology.GetNamespace(),
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: k8scorev1.ServiceSpec{
			Ports: []k8scorev1.ServicePort{
				{
					Name:     "vxlan",
					Protocol: clabernetesconstants.UDP,
					Port:     clabernetesconstants.VXLANServicePort,
					TargetPort: intstr.IntOrString{
						IntVal: clabernetesconstants.VXLANServicePort,
					},
				},
				{
					Name:     "slurpeeth",
					Protocol: clabernetesconstants.TCP,
					Port:     clabernetesconstants.SlurpeethServicePort,
					TargetPort: intstr.IntOrString{
						IntVal: clabernetesconstants.SlurpeethServicePort,
					},
				},
			},
			Selector: selectorLabels,
			Type:     k8scorev1.ServiceTypeClusterIP,
		},
	}
}
