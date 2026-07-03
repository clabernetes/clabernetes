package manager

import (
	clabernetesapisv1alpha1 "github.com/srl-labs/clabernetes/apis/v1alpha1"
	"k8s.io/apimachinery/pkg/labels"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimecache "sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimemetricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func newManager(scheme *apimachineryruntime.Scheme, appName string) (ctrlruntime.Manager, error) {
	return ctrlruntime.NewManager(
		ctrlruntime.GetConfigOrDie(),
		ctrlruntime.Options{
			Logger: klog.NewKlogr(),
			Scheme: scheme,
			Metrics: ctrlruntimemetricsserver.Options{
				BindAddress: "0",
			},
			LeaderElection: false,
			NewCache: func(
				config *rest.Config,
				opts ctrlruntimecache.Options,
			) (ctrlruntimecache.Cache, error) {
				opts.DefaultLabelSelector = labels.SelectorFromSet(
					labels.Set{
						// only cache objects with the "clabernetes/app" label, why would we care
						// about anything else (for now -- and we can override it with opts.ByObject
						// anyway?! and... who the hell calls their app "clabernetes" so this should
						// really limit the cache nicely :)
						// currently this matters for launcher service accounts, role bindings,
						// services (fabric and expose), and (launcher) deployments
						"clabernetes/app": appName,
					},
				)

				opts.ByObject = map[ctrlruntimeclient.Object]ctrlruntimecache.ByObject{
					// obviously we need to cache all "our" topology objects, so do that
					&clabernetesapisv1alpha1.Topology{}: {
						Namespaces: map[string]ctrlruntimecache.Config{
							ctrlruntimecache.AllNamespaces: {
								LabelSelector: labels.Everything(),
							},
						},
					},
					// we need to cache all our image request crs too of course
					&clabernetesapisv1alpha1.ImageRequest{}: {
						Namespaces: map[string]ctrlruntimecache.Config{
							ctrlruntimecache.AllNamespaces: {
								LabelSelector: labels.Everything(),
							},
						},
					},
					// watch our config "singleton" too; while this is sorta/basically a "cluster"
					// CR -- we dont want to have to force users to have cluster wide perms, *and*
					// we want to be able to set an owner ref to the manager deployment, so the
					// config *is* namespaced, so... watch all the namespaces for the config...
					&clabernetesapisv1alpha1.Config{}: {
						Namespaces: map[string]ctrlruntimecache.Config{
							ctrlruntimecache.AllNamespaces: {
								LabelSelector: labels.Everything(),
							},
						},
					},
					// our per (containerlab) node crs
					&clabernetesapisv1alpha1.Node{}: {
						Namespaces: map[string]ctrlruntimecache.Config{
							ctrlruntimecache.AllNamespaces: {
								LabelSelector: labels.Everything(),
							},
						},
					},
					// our per link (tunnel) crs
					&clabernetesapisv1alpha1.Link{}: {
						Namespaces: map[string]ctrlruntimecache.Config{
							ctrlruntimecache.AllNamespaces: {
								LabelSelector: labels.Everything(),
							},
						},
					},
				}

				return ctrlruntimecache.New(config, opts)
			},
		},
	)
}
