package manager

import (
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	k8scorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimecache "sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimemetricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// stripUnmanagedObjectData empties the payload bytes of cached ConfigMaps and Secrets that do
// not carry this release's c9s.run/app label. The cache then serves complete objects for
// c9s-owned resources while holding only metadata for everything else, so payload watches can
// fire without foreign Secret content living in manager memory.
func stripUnmanagedObjectData(appName string) toolscache.TransformFunc {
	return func(obj any) (any, error) {
		switch typed := obj.(type) {
		case *k8scorev1.ConfigMap:
			if typed.GetLabels()["c9s.run/app"] != appName {
				typed.Data = nil
				typed.BinaryData = nil
			}
		case *k8scorev1.Secret:
			if typed.GetLabels()["c9s.run/app"] != appName {
				typed.Data = nil
				typed.StringData = nil
			}
		}

		return obj, nil
	}
}

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
						// only cache objects with the "c9s.run/app" label, why would we care
						// about anything else (for now -- and we can override it with opts.ByObject
						// anyway?! and... who the hell calls their app "clabernetes" so this should
						// really limit the cache nicely :)
						// currently this matters for launcher service accounts, role bindings,
						// services (fabric and expose), and (launcher) deployments
						"c9s.run/app": appName,
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
					// nodes/links/launcher profiles are the primary api -- they are created by
					// users (or tooling) and carry no c9s.run/app label, so they must be
					// cached unconditionally
					&clabernetesapisv1alpha1.Node{}: {
						Namespaces: map[string]ctrlruntimecache.Config{
							ctrlruntimecache.AllNamespaces: {
								LabelSelector: labels.Everything(),
							},
						},
					},
					&clabernetesapisv1alpha1.Link{}: {
						Namespaces: map[string]ctrlruntimecache.Config{
							ctrlruntimecache.AllNamespaces: {
								LabelSelector: labels.Everything(),
							},
						},
					},
					&clabernetesapisv1alpha1.LauncherProfile{}: {
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
					// user-authored payload ConfigMaps/Secrets carry no c9s.run/app label but
					// must still invalidate the Nodes that consume them, so these two types are
					// cached without the default selector. The transform strips data bytes from
					// everything that is not ours: watches fire for payload edits while foreign
					// Secret content never sits in the cache. Reads of unlabeled objects go
					// through the API reader, never this cache.
					&k8scorev1.ConfigMap{}: {
						Namespaces: map[string]ctrlruntimecache.Config{
							ctrlruntimecache.AllNamespaces: {
								LabelSelector: labels.Everything(),
							},
						},
						Transform: stripUnmanagedObjectData(appName),
					},
					&k8scorev1.Secret{}: {
						Namespaces: map[string]ctrlruntimecache.Config{
							ctrlruntimecache.AllNamespaces: {
								LabelSelector: labels.Everything(),
							},
						},
						Transform: stripUnmanagedObjectData(appName),
					},
				}

				return ctrlruntimecache.New(config, opts)
			},
		},
	)
}
