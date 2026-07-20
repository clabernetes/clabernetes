package clicker

import (
	"context"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	claberneteslogging "github.com/clabernetes/clabernetes/logging"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	"gopkg.in/yaml.v3"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	defaultImage = "busybox"
)

// Args holds arguments for the clabernetes "clicker" process.
type Args struct {
	OverrideNodes        bool
	NodeSelector         string
	SkipConfigMapCleanup bool
	SkipPodsCleanup      bool
}

// StartClabernetes is a function that starts the clabernetes node clicker.
func StartClabernetes(args *Args) {
	if clabernetesInstance != nil {
		clabernetesutil.Panic("clabernetes instance already created...")
	}

	rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec

	claberneteslogging.InitManager()

	logManager := claberneteslogging.GetManager()

	clabernetesLogger := logManager.MustRegisterAndGetLogger(
		clabernetesconstants.Clabernetes,
		clabernetesutil.GetEnvStrOrDefault(
			clabernetesconstants.ClickerLoggerLevelEnv,
			clabernetesconstants.Info,
		),
	)

	ctx, _ := clabernetesutil.SignalHandledContext(clabernetesLogger.Criticalf)

	clabernetesInstance = &clabernetes{
		ctx: ctx,
		appName: clabernetesutil.GetEnvStrOrDefault(
			clabernetesconstants.AppNameEnv,
			clabernetesconstants.AppNameDefault,
		),
		logger: clabernetesLogger,
		args:   args,
	}

	err := clabernetesInstance.run()
	if err != nil {
		claberneteslogging.GetManager().Flush()

		os.Exit(clabernetesconstants.ExitCodeError)
	}
}

var clabernetesInstance *clabernetes //nolint:gochecknoglobals

type clabernetes struct {
	ctx context.Context

	appName string

	logger claberneteslogging.Instance

	args *Args

	namespace  string
	kubeConfig *rest.Config
	kubeClient *kubernetes.Clientset
}

func (c *clabernetes) run() error {
	c.logger.Info("starting clabernetes...")

	err := c.setup()
	if err != nil {
		c.logger.Fatal(err.Error())
	}

	selfPod, err := c.getSelfPod()
	if err != nil {
		c.logger.Fatalf("failed fetching our own pod info, err: %s", err)
	}

	targetNodes, err := c.getInvokeNodes()
	if err != nil {
		c.logger.Fatalf("failed fetching cluster nodes, err: %s", err)
	}

	configMap := c.buildConfigMap()

	createdConfigMap, err := c.kubeClient.CoreV1().
		ConfigMaps(c.namespace).
		Create(c.ctx, configMap, metav1.CreateOptions{})
	if err != nil {
		c.logger.Fatalf("failed creating clicker configmap %q, err: %s", configMap.Name, err)
	}

	if !c.args.SkipConfigMapCleanup {
		defer func() {
			err = c.kubeClient.CoreV1().
				ConfigMaps(c.namespace).
				Delete(c.ctx, createdConfigMap.Name, metav1.DeleteOptions{})
			if err != nil {
				c.logger.Criticalf(
					"failed deleting clicker configmap %q, err: %s",
					createdConfigMap.Name,
					err,
				)
			}
		}()
	}

	pods, err := c.buildPods(selfPod, createdConfigMap, targetNodes)
	if err != nil {
		c.logger.Criticalf("failed building clicker pod spec, err: %s", err)

		// no more panicking to exit since we want ot let the defers run if we get this far
		return err
	}

	if !c.args.SkipPodsCleanup {
		defer c.removePods(pods)
	}

	err = c.deployPods(pods)
	if err != nil {
		c.logger.Criticalf("failed creating clicker pods, err: %s", err)

		return err
	}

	wg := &sync.WaitGroup{}

	successChan := make(chan string, len(pods))

	failChan := make(chan string, len(pods))

	for _, pod := range pods {
		wg.Add(1)

		go c.watchPods(wg, successChan, failChan, pod.Name)
	}

	// wait for all jobs to be done
	wg.Wait()

	// update all successfully executed pods
	for len(successChan) > 0 {
		podName := <-successChan

		c.logger.Infof("clicker pod %q was successful!", podName)
	}

	var failed bool

	// log failed pods
	for len(failChan) > 0 {
		failed = true

		podName := <-failChan

		c.logger.Criticalf("clicker pod %q was not successful", podName)
	}

	if failed {
		return fmt.Errorf("%w: one or more workers failed", claberneteserrors.ErrJob)
	}

	return nil
}

func (c *clabernetes) setup() error {
	var err error

	c.namespace, err = clabernetesutilkubernetes.CurrentNamespace()
	if err != nil {
		c.logger.Criticalf("failed getting current namespace, err: %s", err)

		return err
	}

	c.kubeConfig, err = rest.InClusterConfig()
	if err != nil {
		c.logger.Criticalf("failed getting in cluster kubeconfig, err: %s", err)

		return err
	}

	c.kubeClient, err = kubernetes.NewForConfig(c.kubeConfig)
	if err != nil {
		c.logger.Criticalf("failed creating kube client from in cluster kubeconfig, err: %s", err)

		return err
	}

	return nil
}

func (c *clabernetes) getSelfPod() (*k8scorev1.Pod, error) {
	selfPodName := os.Getenv("POD_NAME")

	c.logger.Debugf("determined our pod name is %q", selfPodName)

	pod, err := c.kubeClient.CoreV1().Pods(c.namespace).Get(c.ctx, selfPodName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return pod, nil
}

func (c *clabernetes) getInvokeNodes() ([]k8scorev1.Node, error) {
	listOptions := metav1.ListOptions{
		LabelSelector: c.args.NodeSelector,
	}

	nodesList, err := c.kubeClient.CoreV1().Nodes().List(c.ctx, listOptions)
	if err != nil {
		return nil, err
	}

	nodes := nodesList.Items

	if c.args.OverrideNodes {
		// override set, we want to run on all the nodes
		return nodes, nil
	}

	unconfiguredNodes := make([]k8scorev1.Node, 0)

	for idx := range nodes {
		_, ok := nodes[idx].Labels[clabernetesconstants.LabelClickerNodeConfigured]
		if !ok {
			// clicker configured label wasn't set, we know we need to run on this node
			c.logger.Debugf(
				"node %q does not have clabernetes clicker config label, "+
					"adding to list to configure...",
				nodes[idx].Name,
			)

			unconfiguredNodes = append(unconfiguredNodes, nodes[idx])
		} else {
			c.logger.Infof(
				"node %q already has clabernetes clicker config label, "+
					"not adding to list to configure...",
				nodes[idx].Name,
			)
		}
	}

	return unconfiguredNodes, nil
}

func (c *clabernetes) buildConfigMap() *k8scorev1.ConfigMap {
	return &k8scorev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "clicker-",
			Namespace:    c.namespace,
		},
		Data: map[string]string{
			"script": getScript(),
		},
	}
}

func envToMapStrStr(env string) (map[string]string, error) {
	var out map[string]string

	asStr := os.Getenv(env)
	if asStr == "" {
		return out, nil
	}

	err := yaml.Unmarshal([]byte(asStr), &out)
	if err != nil {
		return nil, err
	}

	return out, nil
}

func envToResources() (k8scorev1.ResourceRequirements, error) {
	out := k8scorev1.ResourceRequirements{}

	asStr := os.Getenv(clabernetesconstants.ClickerWorkerResources)
	if asStr == "" {
		return out, nil
	}

	parsedOut, err := clabernetesutilkubernetes.YAMLToK8sResourceRequirements(asStr)
	if err != nil {
		return out, err
	}

	return *parsedOut, nil
}

func (c *clabernetes) buildPods(
	selfPod *k8scorev1.Pod,
	configMap *k8scorev1.ConfigMap,
	targetNodes []k8scorev1.Node,
) ([]*k8scorev1.Pod, error) {
	image := os.Getenv(clabernetesconstants.ClickerWorkerImage)
	if image == "" {
		image = defaultImage
	}

	globalAnnotations, err := envToMapStrStr(clabernetesconstants.ClickerGlobalAnnotations)
	if err != nil {
		c.logger.Criticalf("failed unmarshalling global annotations for worker pod, error: %s", err)

		return nil, err
	}

	globalLabels, err := envToMapStrStr(clabernetesconstants.ClickerGlobalLabels)
	if err != nil {
		c.logger.Criticalf("failed unmarshalling global labels for worker pod, error: %s", err)

		return nil, err
	}

	resources, err := envToResources()
	if err != nil {
		c.logger.Criticalf("failed building worker pod resources, error: %s", err)

		return nil, err
	}

	pods := make([]*k8scorev1.Pod, 0)

	scriptVolumeName := "clicker-script"

	for idx := range targetNodes {
		nodeName := targetNodes[idx].Name
		name := fmt.Sprintf("%s-clicker-%s", c.appName, nodeName)

		labels := map[string]string{
			clabernetesconstants.LabelApp:               clabernetesconstants.Clabernetes,
			clabernetesconstants.LabelComponent:         "clicker",
			clabernetesconstants.LabelClickerNodeTarget: nodeName,
		}

		maps.Copy(labels, globalLabels)

		pods = append(
			pods,
			&k8scorev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        name,
					Namespace:   c.namespace,
					Annotations: globalAnnotations,
					Labels:      labels,
				},
				Spec: k8scorev1.PodSpec{
					Containers: []k8scorev1.Container{
						{
							Name:                     nodeName,
							WorkingDir:               "/clabernetes",
							Image:                    image,
							Command:                  getCommand(),
							TerminationMessagePath:   "/dev/termination-log",
							TerminationMessagePolicy: "File",
							ImagePullPolicy:          selfPod.Spec.Containers[0].ImagePullPolicy,
							SecurityContext: &k8scorev1.SecurityContext{
								// we need privileged for setting syscalls and such
								Privileged: clabernetesutil.ToPointer(true),
								RunAsUser:  clabernetesutil.ToPointer(int64(0)),
							},
							Env: []k8scorev1.EnvVar{
								{
									Name: clabernetesconstants.LauncherLoggerLevelEnv,
									Value: clabernetesutil.GetEnvStrOrDefault(
										clabernetesconstants.LauncherLoggerLevelEnv,
										clabernetesconstants.Info,
									),
								},
							},
							VolumeMounts: []k8scorev1.VolumeMount{
								{
									Name:      scriptVolumeName,
									ReadOnly:  true,
									MountPath: "/clabernetes/clicker",
									SubPath:   "script",
								},
							},
							Resources: resources,
						},
					},
					RestartPolicy: "Never",
					NodeSelector: map[string]string{
						"kubernetes.io/hostname": nodeName,
					},
					ServiceAccountName: selfPod.Spec.ServiceAccountName,
					Volumes: []k8scorev1.Volume{
						{
							Name: scriptVolumeName,
							VolumeSource: k8scorev1.VolumeSource{
								ConfigMap: &k8scorev1.ConfigMapVolumeSource{
									LocalObjectReference: k8scorev1.LocalObjectReference{
										Name: configMap.Name,
									},
								},
							},
						},
					},
				},
			},
		)
	}

	return pods, nil
}

func (c *clabernetes) deployPods(pods []*k8scorev1.Pod) error {
	for _, pod := range pods {
		_, err := c.kubeClient.CoreV1().
			Pods(c.namespace).
			Create(c.ctx, pod, metav1.CreateOptions{})
		if err != nil {
			c.logger.Criticalf(
				"failed creating clicker pod for node %q, err: %s",
				pod.Labels[clabernetesconstants.LabelClickerNodeTarget],
				err,
			)

			return err
		}
	}

	return nil
}

func (c *clabernetes) watchPods(
	wg *sync.WaitGroup,
	successChan chan string,
	failChan chan string,
	podName string,
) {
	defer wg.Done()

	listOptions := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", podName),
		Watch:         true,
	}

	watch, err := c.kubeClient.CoreV1().Pods(c.namespace).Watch(c.ctx, listOptions)
	if err != nil {
		c.logger.Criticalf("failed watching clicker pod %q, err: %s", podName, err)

		failChan <- podName

		return
	}

	for event := range watch.ResultChan() {
		eventPod, ok := event.Object.(*k8scorev1.Pod)
		if !ok {
			c.logger.Debug(
				"watch event is not type pod, probably due to stream closing, ignoring",
			)

			break
		}

		switch eventPod.Status.Phase { //nolint:exhaustive
		case k8scorev1.PodSucceeded:
			c.logger.Debugf("pod %q reports succeeded", podName)

			watch.Stop()

			nodeName := strings.TrimPrefix(podName, fmt.Sprintf("%s-clicker-", c.appName))

			err = c.updateNodeLabels(nodeName)
			if err != nil {
				failChan <- podName
			} else {
				successChan <- podName
			}
		case k8scorev1.PodFailed:
			c.logger.Criticalf("pod %q reports failed", podName)

			watch.Stop()

			failChan <- podName
		}
	}
}

func (c *clabernetes) removePods(pods []*k8scorev1.Pod) {
	for _, pod := range pods {
		err := c.kubeClient.CoreV1().
			Pods(c.namespace).
			Delete(c.ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			c.logger.Criticalf("failed creating clicker pod %q, err: %s", pod.Name, err)
		}
	}
}

func (c *clabernetes) updateNodeLabels(nodeName string) error {
	patch := fmt.Sprintf(
		`[{"op": "add", "path": "/metadata/labels/%s", "value": "%d"}]`,
		// have to replace the slash in the label name for jsonpatch
		strings.ReplaceAll(clabernetesconstants.LabelClickerNodeConfigured, "/", "~1"),
		time.Now().Unix(),
	)

	_, err := c.kubeClient.CoreV1().
		Nodes().
		Patch(
			c.ctx,
			nodeName,
			apimachinerytypes.JSONPatchType,
			[]byte(patch),
			metav1.PatchOptions{},
		)
	if err != nil {
		c.logger.Criticalf("failed updating node %q label, error: %s", nodeName, err)
	}

	return err
}
