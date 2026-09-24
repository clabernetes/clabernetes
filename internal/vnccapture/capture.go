// Package vnccapture runs plan-authorized clabwire captures from a user's Kubernetes client.
package vnccapture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesgeneratedclientset "github.com/clabernetes/clabernetes/generated/clientset"
	clabernetesinternaldirectpod "github.com/clabernetes/clabernetes/internal/directpod"
	clabernetesinternaldirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	k8scorev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clientgorest "k8s.io/client-go/rest"
	clientgoportforward "k8s.io/client-go/tools/portforward"
	clientgoremotecommand "k8s.io/client-go/tools/remotecommand"
	clientgospdy "k8s.io/client-go/transport/spdy"
)

const (
	// DefaultVNCImage is the maintained Wireshark/noVNC image.
	DefaultVNCImage  = "ghcr.io/srl-labs/wireshark-vnc-docker:latest"
	capturePipePath  = "/pcaps/clabwire.pcap"
	vncContainerName = "wireshark"
	vncPort          = 5800
)

// Options describes one local capture command invocation.
type Options struct {
	Namespace   string
	NodeName    string
	Interface   string
	Duration    time.Duration
	PacketLimit int
	SnapLength  int
	VNC         bool
	VNCImage    string
	LocalPort   int
	Out         io.Writer
	ErrOut      io.Writer
}

// Run resolves the current direct workload and streams its authorized capture to stdout or VNC.
func Run(ctx context.Context, config *clientgorest.Config, options Options) error {
	if config == nil {
		return errors.New("Kubernetes client configuration is nil")
	}

	options.Namespace = strings.TrimSpace(options.Namespace)
	options.NodeName = strings.TrimSpace(options.NodeName)
	options.Interface = strings.TrimSpace(options.Interface)
	if options.Namespace == "" || options.NodeName == "" || options.Interface == "" {
		return errors.New("capture namespace, Node, and interface are required")
	}
	if options.Out == nil {
		options.Out = io.Discard
	}
	if options.ErrOut == nil {
		options.ErrOut = io.Discard
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("creating Kubernetes client: %w", err)
	}
	c9sClient, err := clabernetesgeneratedclientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("creating Clabernetes client: %w", err)
	}

	node, err := c9sClient.C9sV1alpha1().Nodes(options.Namespace).Get(
		ctx,
		options.NodeName,
		metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("getting Node %s/%s: %w", options.Namespace, options.NodeName, err)
	}

	captureOptions, err := clabernetesinternaldirectruntime.NormalizePacketCaptureOptions(
		clabernetesinternaldirectruntime.PacketCaptureOptions{
			NodeID:        string(node.GetUID()),
			InterfaceName: options.Interface,
			SnapLength:    options.SnapLength,
			PacketLimit:   options.PacketLimit,
			Duration:      options.Duration,
		},
	)
	if err != nil {
		return err
	}

	devicePod, err := ResolveDevicePod(ctx, kubeClient, options.Namespace, options.NodeName)
	if err != nil {
		return err
	}
	command := CaptureCommand(captureOptions)

	if !options.VNC {
		return streamExec(
			ctx,
			config,
			kubeClient,
			options.Namespace,
			devicePod,
			clabernetesinternaldirectpod.ConnectivityContainerName,
			command,
			nil,
			options.Out,
			options.ErrOut,
		)
	}

	return runVNCSession(ctx, config, kubeClient, devicePod, command, options)
}

// CaptureCommand builds the fixed in-clabwire command. The runtime validates the target against
// its mounted accepted plan and connectivity revision before opening the interface.
func CaptureCommand(options clabernetesinternaldirectruntime.PacketCaptureOptions) []string {
	command := []string{
		"/clabernetes/manager",
		"node-runtime",
		"packet-capture",
		"--plan",
		"/var/run/clabernetes/plan/plan.json",
		"--input",
		"/var/run/clabernetes/input/input.json",
		"--connectivityRevision",
		"/var/run/clabernetes/connectivity-revision/revision.json",
		"--nodeID",
		options.NodeID,
		"--interface",
		options.InterfaceName,
		"--snapLength",
		strconv.Itoa(options.SnapLength),
	}
	if options.PacketLimit != 0 {
		command = append(command, "--packetLimit", strconv.Itoa(options.PacketLimit))
	}
	if options.Duration != 0 {
		command = append(command, "--duration", options.Duration.String())
	}

	return command
}

// ResolveDevicePod follows the Node's fabric Service selector so grouped logical Nodes resolve to
// their primary direct workload too.
func ResolveDevicePod(
	ctx context.Context,
	client kubernetes.Interface,
	namespace,
	nodeName string,
) (string, error) {
	service, err := client.CoreV1().Services(namespace).Get(
		ctx,
		nodeName+"-wire",
		metav1.GetOptions{},
	)
	if err != nil && !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("getting Node fabric Service: %w", err)
	}

	selector := map[string]string{
		clabernetesconstants.LabelDirectWorkload: nodeName,
	}
	if err == nil && len(service.Spec.Selector) != 0 {
		selector = service.Spec.Selector
	}

	pods, err := client.CoreV1().Pods(namespace).List(
		ctx,
		metav1.ListOptions{LabelSelector: labels.SelectorFromSet(selector).String()},
	)
	if err != nil {
		return "", fmt.Errorf("listing direct workload Pods: %w", err)
	}

	running := make([]string, 0, len(pods.Items))
	ready := make([]string, 0, len(pods.Items))
	for index := range pods.Items {
		pod := &pods.Items[index]
		if pod.GetDeletionTimestamp() == nil && pod.Status.Phase == k8scorev1.PodRunning {
			running = append(running, pod.GetName())
			for _, condition := range pod.Status.Conditions {
				if condition.Type == k8scorev1.PodReady &&
					condition.Status == k8scorev1.ConditionTrue {
					ready = append(ready, pod.GetName())

					break
				}
			}
		}
	}
	if len(ready) == 1 {
		return ready[0], nil
	}
	if len(running) != 1 {
		return "", fmt.Errorf(
			"Node %s/%s resolved to %d running direct workload Pods",
			namespace,
			nodeName,
			len(running),
		)
	}

	return running[0], nil
}

// RenderVNCPod renders a capability-free, non-restarting Wireshark/noVNC session.
func RenderVNCPod(namespace, image string) *k8scorev1.Pod {
	falseValue := false
	fsGroup := int64(1000)

	return &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: "c9s-wireshark-",
			Labels: map[string]string{
				clabernetesconstants.LabelApp:       clabernetesconstants.Clabernetes,
				clabernetesconstants.LabelComponent: "wireshark-capture",
			},
		},
		Spec: k8scorev1.PodSpec{
			AutomountServiceAccountToken: &falseValue,
			RestartPolicy:                k8scorev1.RestartPolicyNever,
			SecurityContext: &k8scorev1.PodSecurityContext{
				FSGroup: &fsGroup,
			},
			Containers: []k8scorev1.Container{{
				Name:            vncContainerName,
				Image:           image,
				ImagePullPolicy: k8scorev1.PullIfNotPresent,
				Env: []k8scorev1.EnvVar{{
					Name:  "CLABWIRE_CAPTURE_PIPE",
					Value: capturePipePath,
				}},
				Ports: []k8scorev1.ContainerPort{{
					Name:          "novnc",
					ContainerPort: vncPort,
					Protocol:      k8scorev1.ProtocolTCP,
				}},
				ReadinessProbe: &k8scorev1.Probe{
					ProbeHandler: k8scorev1.ProbeHandler{
						Exec: &k8scorev1.ExecAction{Command: []string{
							"sh", "-c", "test -p \"${CLABWIRE_CAPTURE_PIPE:?}\"",
						}},
					},
					PeriodSeconds:    1,
					TimeoutSeconds:   1,
					FailureThreshold: 120,
				},
				SecurityContext: &k8scorev1.SecurityContext{
					AllowPrivilegeEscalation: &falseValue,
					Capabilities: &k8scorev1.Capabilities{
						Drop: []k8scorev1.Capability{"NET_RAW"},
					},
				},
				VolumeMounts: []k8scorev1.VolumeMount{{
					Name: "pcaps", MountPath: "/pcaps",
				}},
			}},
			Volumes: []k8scorev1.Volume{{
				Name: "pcaps", VolumeSource: emptyDirectory(),
			}},
			EnableServiceLinks: &falseValue,
		},
	}
}

func runVNCSession(
	ctx context.Context,
	config *clientgorest.Config,
	client kubernetes.Interface,
	devicePod string,
	command []string,
	options Options,
) (resultErr error) {
	if strings.TrimSpace(options.VNCImage) == "" {
		return errors.New("VNC image is required")
	}
	if options.LocalPort < 0 || options.LocalPort > 65535 {
		return errors.New("VNC local port is outside the supported range")
	}

	pod, err := client.CoreV1().Pods(options.Namespace).Create(
		ctx,
		RenderVNCPod(options.Namespace, options.VNCImage),
		metav1.CreateOptions{},
	)
	if err != nil {
		return fmt.Errorf("creating Wireshark VNC Pod: %w", err)
	}
	defer func() {
		if resultErr != nil {
			tailLines := int64(100)
			logContext, cancelLogs := context.WithTimeout(context.Background(), 10*time.Second)
			logs, logErr := client.CoreV1().Pods(pod.GetNamespace()).GetLogs(
				pod.GetName(),
				&k8scorev1.PodLogOptions{
					Container: vncContainerName,
					TailLines: &tailLines,
				},
			).DoRaw(logContext)
			cancelLogs()
			if logErr == nil && strings.TrimSpace(string(logs)) != "" {
				resultErr = fmt.Errorf(
					"%w: Wireshark VNC logs:\n%s",
					resultErr,
					strings.TrimSpace(string(logs)),
				)
			}
		}
		deleteSessionPod(client, pod)
	}()

	if err = waitForReady(ctx, client, pod.GetNamespace(), pod.GetName()); err != nil {
		return err
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	forwarder, forwardErrors, stopForward, err := startPortForward(
		sessionCtx,
		config,
		client,
		pod.GetNamespace(),
		pod.GetName(),
		options.LocalPort,
		options.ErrOut,
	)
	if err != nil {
		return err
	}
	defer stopForward()

	ports, err := forwarder.GetPorts()
	if err != nil {
		return fmt.Errorf("resolving Wireshark VNC local port: %w", err)
	}
	if len(ports) != 1 {
		return errors.New("Wireshark VNC port forward returned no local port")
	}
	_, _ = fmt.Fprintf(
		options.ErrOut,
		"Wireshark VNC: http://127.0.0.1:%d\n",
		ports[0].Local,
	)

	captureDone := make(chan error, 1)
	go func() {
		captureDone <- streamCaptureToVNC(
			sessionCtx,
			config,
			client,
			options.Namespace,
			devicePod,
			pod.GetName(),
			command,
			options.ErrOut,
		)
	}()

	select {
	case err = <-captureDone:
		return err
	case forwardErr := <-forwardErrors:
		cancel()
		captureErr := <-captureDone
		if forwardErr != nil {
			return fmt.Errorf("forwarding Wireshark VNC port: %w", forwardErr)
		}

		return captureErr
	case <-ctx.Done():
		cancel()
		<-captureDone

		return ctx.Err()
	}
}

func streamCaptureToVNC(
	ctx context.Context,
	config *clientgorest.Config,
	client kubernetes.Interface,
	namespace,
	devicePod,
	vncPod string,
	command []string,
	audit io.Writer,
) error {
	streamContext, cancel := context.WithCancel(ctx)
	defer cancel()

	reader, writer := io.Pipe()
	sinkErrors := make(chan error, 1)

	go func() {
		var stderr bytes.Buffer
		err := streamExec(
			streamContext,
			config,
			client,
			namespace,
			vncPod,
			vncContainerName,
			[]string{"sh", "-c", "cat > \"${CLABWIRE_CAPTURE_PIPE:?}\""},
			reader,
			io.Discard,
			&stderr,
		)
		_ = reader.CloseWithError(err)
		if err != nil {
			err = fmt.Errorf("feeding Wireshark capture pipe: %w: %s", err, stderr.String())
		}
		sinkErrors <- err
	}()

	sourceErr := streamExec(
		streamContext,
		config,
		client,
		namespace,
		devicePod,
		clabernetesinternaldirectpod.ConnectivityContainerName,
		command,
		nil,
		writer,
		audit,
	)
	_ = writer.CloseWithError(sourceErr)
	if sourceErr != nil {
		cancel()

		return fmt.Errorf("streaming clabwire capture: %w", sourceErr)
	}

	select {
	case sinkErr := <-sinkErrors:
		return sinkErr
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		cancel()

		return errors.New("timed out closing the Wireshark capture pipe")
	}
}

func streamExec(
	ctx context.Context,
	config *clientgorest.Config,
	client kubernetes.Interface,
	namespace,
	pod,
	container string,
	command []string,
	stdin io.Reader,
	stdout,
	stderr io.Writer,
) error {
	request := client.CoreV1().RESTClient().Post().
		Namespace(namespace).
		Resource("pods").
		Name(pod).
		SubResource("exec").
		VersionedParams(&k8scorev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     stdin != nil,
			Stdout:    stdout != nil,
			Stderr:    stderr != nil,
		}, clientgoscheme.ParameterCodec)
	executor, err := clientgoremotecommand.NewSPDYExecutor(
		config,
		http.MethodPost,
		request.URL(),
	)
	if err != nil {
		return fmt.Errorf("creating Pod executor: %w", err)
	}

	return executor.StreamWithContext(ctx, clientgoremotecommand.StreamOptions{
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
	})
}

func waitForReady(
	ctx context.Context,
	client kubernetes.Interface,
	namespace,
	name string,
) error {
	err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 2*time.Minute, true,
		func(ctx context.Context) (bool, error) {
			pod, getErr := client.CoreV1().Pods(namespace).Get(
				ctx,
				name,
				metav1.GetOptions{},
			)
			if getErr != nil {
				return false, getErr
			}
			if pod.Status.Phase == k8scorev1.PodFailed {
				return false, errors.New("Wireshark VNC Pod failed before becoming ready")
			}
			for _, condition := range pod.Status.Conditions {
				if condition.Type == k8scorev1.PodReady &&
					condition.Status == k8scorev1.ConditionTrue {
					return true, nil
				}
			}

			return false, nil
		})
	if err != nil {
		return fmt.Errorf("waiting for Wireshark VNC Pod %s/%s: %w", namespace, name, err)
	}

	return nil
}

func startPortForward(
	ctx context.Context,
	config *clientgorest.Config,
	client kubernetes.Interface,
	namespace,
	pod string,
	localPort int,
	errorsOutput io.Writer,
) (
	*clientgoportforward.PortForwarder,
	<-chan error,
	func(),
	error,
) {
	transport, upgrader, err := clientgospdy.RoundTripperFor(config)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating port-forward transport: %w", err)
	}
	request := client.CoreV1().RESTClient().Post().
		Namespace(namespace).
		Resource("pods").
		Name(pod).
		SubResource("portforward")
	dialer := clientgospdy.NewDialer(
		upgrader,
		&http.Client{Transport: transport},
		http.MethodPost,
		request.URL(),
	)
	stopChannel := make(chan struct{})
	readyChannel := make(chan struct{})
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() { close(stopChannel) })
	}
	go func() {
		<-ctx.Done()
		stop()
	}()

	port := strconv.Itoa(localPort) + ":" + strconv.Itoa(vncPort)
	forwarder, err := clientgoportforward.New(
		dialer,
		[]string{port},
		stopChannel,
		readyChannel,
		io.Discard,
		errorsOutput,
	)
	if err != nil {
		stop()

		return nil, nil, nil, fmt.Errorf("creating Wireshark VNC port forward: %w", err)
	}
	forwardErrors := make(chan error, 1)
	go func() {
		forwardErrors <- forwarder.ForwardPorts()
	}()

	select {
	case <-readyChannel:
		return forwarder, forwardErrors, stop, nil
	case forwardErr := <-forwardErrors:
		stop()

		return nil, nil, nil, fmt.Errorf("starting Wireshark VNC port forward: %w", forwardErr)
	case <-ctx.Done():
		stop()

		return nil, nil, nil, ctx.Err()
	}
}

func deleteSessionPod(client kubernetes.Interface, pod *k8scorev1.Pod) {
	deleteContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	grace := int64(0)
	_ = client.CoreV1().Pods(pod.GetNamespace()).Delete(
		deleteContext,
		pod.GetName(),
		metav1.DeleteOptions{GracePeriodSeconds: &grace},
	)
}

func emptyDirectory() k8scorev1.VolumeSource {
	return k8scorev1.VolumeSource{EmptyDir: &k8scorev1.EmptyDirVolumeSource{}}
}
