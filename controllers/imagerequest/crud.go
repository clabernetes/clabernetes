package imagerequest

import (
	"context"

	clabernetesapis "github.com/clabernetes/clabernetes/apis"
	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
)

// getTopologyFromReq fetches the reconcile target Topology from the Request.
func (c *Controller) getImageRequestFromReq(
	ctx context.Context,
	req ctrlruntime.Request,
) (*clabernetesapisv1alpha1.ImageRequest, error) {
	imageRequest := &clabernetesapisv1alpha1.ImageRequest{}

	err := c.BaseController.Client.Get(
		ctx,
		apimachinerytypes.NamespacedName{
			Namespace: req.Namespace,
			Name:      req.Name,
		},
		imageRequest,
	)

	return imageRequest, err
}

func (c *Controller) update(
	ctx context.Context,
	imageRequest *clabernetesapisv1alpha1.ImageRequest,
) error {
	c.Log.Debugf(
		"updating %s '%s/%s'",
		clabernetesapis.ImageRequest,
		imageRequest.GetNamespace(),
		imageRequest.GetName(),
	)

	err := c.Client.Update(ctx, imageRequest)
	if err != nil {
		c.Log.Criticalf(
			"failed updating %s '%s/%s' error: %s",
			clabernetesapis.ImageRequest,
			imageRequest.GetNamespace(),
			imageRequest.GetName(),
			err,
		)

		return err
	}

	return nil
}

func (c *Controller) delete(
	ctx context.Context,
	imageRequest *clabernetesapisv1alpha1.ImageRequest,
) error {
	c.Log.Debugf(
		"deleting %s '%s/%s'",
		clabernetesapis.ImageRequest,
		imageRequest.GetNamespace(),
		imageRequest.GetName(),
	)

	err := c.Client.Delete(ctx, imageRequest)
	if err != nil {
		c.Log.Criticalf(
			"failed deleting %s '%s/%s' error: %s",
			clabernetesapis.ImageRequest,
			imageRequest.GetNamespace(),
			imageRequest.GetName(),
			err,
		)

		return err
	}

	return nil
}
