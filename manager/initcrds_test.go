package manager //nolint:testpackage // Tests intentionally verify the unexported migration helper.

import (
	"context"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
)

func TestRemoveLegacyCrdsWaitsForLauncherProfileReplacement(t *testing.T) {
	for _, testCase := range []struct {
		name                    string
		replacementInstalled    bool
		expectNodeProfileDelete bool
	}{
		{
			name:                    "replacement missing",
			replacementInstalled:    false,
			expectNodeProfileDelete: false,
		},
		{
			name:                    "replacement installed",
			replacementInstalled:    true,
			expectNodeProfileDelete: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			objects := []apimachineryruntime.Object{
				&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{
					Name: "connectivities.clabernetes.containerlab.dev",
				}},
				&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{
					Name: "nodeprofiles.clabernetes.containerlab.dev",
				}},
			}
			if testCase.replacementInstalled {
				objects = append(objects, &apiextensionsv1.CustomResourceDefinition{
					ObjectMeta: metav1.ObjectMeta{
						Name: "launcherprofiles.clabernetes.containerlab.dev",
					},
				})
			}

			client := apiextensionsfake.NewSimpleClientset(objects...)

			err := removeLegacyCrds(context.Background(), client)
			if err != nil {
				t.Fatalf("removing legacy CRDs failed: %s", err)
			}

			_, err = client.ApiextensionsV1().
				CustomResourceDefinitions().
				Get(
					context.Background(),
					"connectivities.clabernetes.containerlab.dev",
					metav1.GetOptions{},
				)
			if !apimachineryerrors.IsNotFound(err) {
				t.Fatalf("expected legacy Connectivity CRD deleted, got: %v", err)
			}

			_, err = client.ApiextensionsV1().
				CustomResourceDefinitions().
				Get(
					context.Background(),
					"nodeprofiles.clabernetes.containerlab.dev",
					metav1.GetOptions{},
				)
			if testCase.expectNodeProfileDelete && !apimachineryerrors.IsNotFound(err) {
				t.Fatalf("expected obsolete NodeProfile CRD deleted, got: %v", err)
			}

			if !testCase.expectNodeProfileDelete && err != nil {
				t.Fatalf("expected NodeProfile CRD retained until replacement exists: %s", err)
			}
		})
	}
}
