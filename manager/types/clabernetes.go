package types

import (
	"context"

	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	clabernetesinternaldeviceruntime "github.com/clabernetes/clabernetes/internal/deviceruntime"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Clabernetes is an interface that defines the publicly available methods of the manager object.
type Clabernetes interface { //nolint: interfacebloat
	// GetContext returns the main/root context for the manager.
	GetContext() context.Context

	// GetContextCancel returns the cancel func for the main/root context for the manager.
	GetContextCancel() context.CancelFunc

	// GetAppName returns the "appName" for the clabernetes deployment, usually "clabernetes".
	GetAppName() string

	// GetBaseLogger returns the base/clabernetes claberneteslogging.Instance.
	GetBaseLogger() claberneteslogging.Instance

	// GetNamespace returns the namespace the clabernetes instance is running in.
	GetNamespace() string

	// GetDeviceRuntimeMode returns the explicitly selected temporary migration runtime.
	GetDeviceRuntimeMode() clabernetesinternaldeviceruntime.Mode

	// GetDeviceRuntimeImage returns the c9s manager image used by direct-runtime workers/helpers.
	GetDeviceRuntimeImage() string

	// IsInitializer returns true if the clabernetes instance is an initializer instance -- if true
	// this means that this instance should update crds, webhook configurations, and other
	// initialization resources.
	IsInitializer() bool

	// GetKubeConfig returns the in-cluster rest.Config for the clabernetes instance.
	GetKubeConfig() *rest.Config

	// GetKubeClient returns the in-cluster kubernetes.Clientset for the clabernetes instance.
	GetKubeClient() *kubernetes.Clientset

	// GetKubeClabernetesClient returns the in-cluster clabernetes Clientset client.
	GetKubeClabernetesClient() *clabernetesgeneratedclientset.Clientset

	// GetCtrlRuntimeMgr returns the controller-runtime Manager for the clabernetes instance.
	GetCtrlRuntimeMgr() ctrlruntime.Manager

	// GetScheme returns apimachineryruntime.Scheme in use by the manager.
	GetScheme() *apimachineryruntime.Scheme

	// GetCtrlRuntimeClient returns the controller-runtime client.
	GetCtrlRuntimeClient() ctrlruntimeclient.Client

	// NewContextWithTimeout returns a new context and its cancelFunc from the base clabernetes
	// context.
	NewContextWithTimeout() (context.Context, context.CancelFunc)

	// IsReady returns the readiness state of the manager.
	IsReady() bool

	// Exit flushes the logging manager and exits with the given exit code.
	Exit(exitCode int)
}
