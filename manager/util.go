package manager

import (
	"fmt"

	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func getCertificatesSecret(c clabernetesmanagertypes.Clabernetes) (*k8scorev1.Secret, error) {
	ctx, ctxCancel := c.NewContextWithTimeout()
	defer ctxCancel()

	return c.GetKubeClient().CoreV1().Secrets(c.GetNamespace()).Get(
		ctx,
		fmt.Sprintf("%s-certificate", c.GetAppName()),
		metav1.GetOptions{},
	)
}
