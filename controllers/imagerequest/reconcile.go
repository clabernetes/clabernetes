package imagerequest

import (
	"context"
	"fmt"
	"maps"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerywatch "k8s.io/apimachinery/pkg/watch"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	puller = "puller"
)

// Reconcile handles reconciliation for this controller.
func (c *Controller) Reconcile(
	ctx context.Context,
	req ctrlruntime.Request,
) (ctrlruntime.Result, error) {
	c.BaseController.LogReconcileStart(req)

	imageRequest, err := c.getImageRequestFromReq(ctx, req)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			c.BaseController.LogReconcileCompleteObjectNotExist(req)

			return ctrlruntime.Result{}, nil
		}

		c.BaseController.LogReconcileFailedGettingObject(req, err)

		return ctrlruntime.Result{}, err
	}

	if imageRequest.DeletionTimestamp != nil {
		return ctrlruntime.Result{}, nil
	}

	if !imageRequest.Status.Accepted {
		// set "accepted" so the launcher knows that the controller has seen the request
		imageRequest.Status.Accepted = true

		err = c.update(ctx, imageRequest)
		if err != nil {
			return ctrlruntime.Result{}, err
		}

		// just requeue, we won't enter this block again anyway
		return ctrlruntime.Result{Requeue: true}, nil
	}

	pullerPodName, err := c.spawnImagePullerPod(ctx, imageRequest)
	if err != nil {
		return ctrlruntime.Result{}, err
	}

	err = c.waitImagePullerPodOutOfPending(ctx, imageRequest.Namespace, pullerPodName)
	if err != nil {
		return ctrlruntime.Result{}, err
	}

	err = c.deleteImagePullerPod(ctx, imageRequest.Namespace, pullerPodName)
	if err != nil {
		return ctrlruntime.Result{}, err
	}

	if !imageRequest.Status.Complete {
		imageRequest.Status.Complete = true

		err = c.update(ctx, imageRequest)
		if err != nil {
			return ctrlruntime.Result{}, err
		}
	}

	// we've done the job of the puller pod, delete the cr
	err = c.delete(ctx, imageRequest)
	if err != nil {
		return ctrlruntime.Result{}, err
	}

	return ctrlruntime.Result{}, nil
}

func (c *Controller) spawnImagePullerPod(
	ctx context.Context,
	imageRequest *clabernetesapisv1alpha1.ImageRequest,
) (string, error) {
	globalAnnotations, globalLabels := clabernetesconfig.GetManager().GetAllMetadata()

	imageHash := clabernetesutil.HashBytes([]byte(imageRequest.Spec.RequestedImage))

	selectorLabels := map[string]string{
		clabernetesconstants.LabelApp: clabernetesconstants.Clabernetes,
		clabernetesconstants.LabelName: fmt.Sprintf(
			"%s-%s",
			clabernetesconstants.Clabernetes,
			puller,
		),
		clabernetesconstants.LabelTopologyOwner:    imageRequest.Spec.TopologyName,
		clabernetesconstants.LabelTopologyNode:     imageRequest.Spec.TopologyNodeName,
		clabernetesconstants.LabelPullerNodeTarget: imageRequest.Spec.KubernetesNode,
		clabernetesconstants.LabelPullerImageHash:  imageHash[:13],
	}

	labels := make(map[string]string)

	maps.Copy(labels, selectorLabels)
	maps.Copy(labels, globalLabels)

	annotations := map[string]string{
		// image string wont be valid for a label, so we'll put it here
		"clabernetes/pullerRequestedImage": imageRequest.Spec.RequestedImage,
	}

	maps.Copy(annotations, globalAnnotations)

	requestedPullSecrets := make(
		[]k8scorev1.LocalObjectReference,
		len(imageRequest.Spec.RequestedImagePullSecrets),
	)

	for idx, pullSecret := range imageRequest.Spec.RequestedImagePullSecrets {
		requestedPullSecrets[idx] = k8scorev1.LocalObjectReference{Name: pullSecret}
	}

	pullerPod := &k8scorev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: clabernetesutilkubernetes.SafeConcatNameKubernetes(
				clabernetesconstants.Clabernetes,
				puller,
				imageRequest.Spec.KubernetesNode,
				imageHash,
			),
			Namespace:   imageRequest.Namespace,
			Annotations: annotations,
			Labels:      labels,
		},
		Spec: k8scorev1.PodSpec{
			Containers: []k8scorev1.Container{
				{
					Name:  "puller",
					Image: imageRequest.Spec.RequestedImage,
					// we don't care if it runs, only care if we can pull the image...
					Command: []string{
						"exit",
						"0",
					},
					ImagePullPolicy: "IfNotPresent",
				},
			},
			NodeName:         imageRequest.Spec.KubernetesNode,
			ImagePullSecrets: requestedPullSecrets,
		},
	}

	// always set the owner ref to ensure that when we delete this imageRequest cr the pod will
	// get deleted (even if dont explicitly do so for some reason)
	err := ctrlruntimeutil.SetOwnerReference(imageRequest, pullerPod, c.Client.Scheme())
	if err != nil {
		return "", err
	}

	err = c.Client.Create(
		ctx,
		pullerPod,
	)
	if err != nil {
		c.Log.Criticalf(
			"failed creating image puller pod for image %q, node %q, error: %s",
			imageRequest.Spec.RequestedImage,
			imageRequest.Spec.KubernetesNode,
			err,
		)

		return "", err
	}

	return pullerPod.Name, nil
}

func (c *Controller) waitImagePullerPodOutOfPending(
	ctx context.Context,
	namespace, pullerPodName string,
) error {
	listOptions := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", pullerPodName),
		Watch:         true,
	}

	watch, err := c.KubeClient.CoreV1().Pods(namespace).Watch(ctx, listOptions)
	if err != nil {
		return err
	}

	for event := range watch.ResultChan() {
		switch event.Type { //nolint:exhaustive
		case apimachinerywatch.Added, apimachinerywatch.Modified:
			pod, ok := event.Object.(*k8scorev1.Pod)
			if !ok {
				panic("this is a bug in the puller pod watch, this should not happen")
			}

			switch pod.Status.Phase { //nolint:exhaustive
			case k8scorev1.PodPending:
				// pending so it hasnt been scheduled, and certainly hasnt pulled the image yet...
				continue
			case k8scorev1.PodRunning, k8scorev1.PodSucceeded, k8scorev1.PodFailed:
				// its running/succeeded/failed any of which means the image has been pulled and
				// we can be done/kill the pod now
				c.Log.Infof(
					"puller pod '%s/%s' has left pending state, image pulled",
					namespace,
					pullerPodName,
				)

				watch.Stop()
			}
		}
	}

	return nil
}

func (c *Controller) deleteImagePullerPod(
	ctx context.Context,
	namespace, pullerPodName string,
) error {
	c.Log.Debugf("destroying image puller pod %q", pullerPodName)

	err := c.Client.Delete(
		ctx,
		&k8scorev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      pullerPodName,
			},
		},
	)
	if err != nil {
		if apimachineryerrors.IsNotFound(err) {
			c.Log.Warnf(
				"puller pod '%s/%s' not found when attempting to delete, this is probably"+
					" ok as it is either deleted or will be when we delete this image request cr",
				namespace, pullerPodName,
			)

			return nil
		}

		c.Log.Criticalf(
			"failed deleting image puller pod '%s/%s', error: %s", namespace, pullerPodName, err,
		)

		return err
	}

	return nil
}
