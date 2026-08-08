package suite

import (
	"fmt"
	"testing"
	"time"

	clabernetestesthelper "github.com/srl-labs/clabernetes/testhelper"
)

const (
	updateReconcileWait    = 30 * time.Second
	eventuallyPollInterval = 3 * time.Second
	eventuallyMaxTime      = 120 * time.Second
)

// Run executes a clabernetes e2e test.
func Run(t *testing.T, testName string, steps []Step, namespace string) { //nolint: thelper
	clabernetestesthelper.KubectlCreateNamespace(t, namespace)

	defer func() {
		if !*clabernetestesthelper.SkipCleanup {
			t.Logf("deleting namespace %q used in test %q", namespace, testName)

			clabernetestesthelper.KubectlDeleteNamespace(t, namespace)
		}
	}()

	for _, step := range steps {
		t.Log(LogStepDescr(step.Index, step.Description))

		stepFixtures := GlobStepFixtures(t, step.Index)

		for _, stepFixture := range stepFixtures {
			stepFixtureOperationType := GetStepFixtureType(t, stepFixture)

			clabernetestesthelper.KubectlFileOp(t, stepFixtureOperationType, namespace, stepFixture)
		}

		if *clabernetestesthelper.Update {
			// update is true, wait before fetching objects a bit to make sure things have had
			// time to reconcile fully
			time.Sleep(updateReconcileWait)
		}

		for kind, objects := range step.AssertObjects {
			for idx := range objects {
				object := step.AssertObjects[kind][idx]

				t.Logf("\tbegin assertion of %q resources %q", kind, object.Name)

				fileName := fmt.Sprintf("golden/%d-%s.%s.yaml", step.Index, kind, object.Name)

				if *clabernetestesthelper.Update {
					// updating so no reason to fetch/compare object
					objectData := getter(t, namespace, kind, object.Name, object)

					clabernetestesthelper.WriteTestFixtureFile(t, fileName, objectData)
				} else {
					eventually(
						t,
						eventuallyPollInterval,
						eventuallyMaxTime,
						func() []byte {
							return getter(t, namespace, kind, object.Name, object)
						},
						clabernetestesthelper.ReadTestFixtureFile(t, fileName),
					)
				}
			}
		}

		t.Log(LogStepSuccess(step.Index))
	}
}

func getter(t *testing.T, namespace, kind, objectName string, object AssertObject) []byte {
	t.Helper()

	objectData := clabernetestesthelper.KubectlGetOp(t, kind, namespace, objectName)

	if !object.SkipDefaultNormalize {
		objectData = clabernetestesthelper.NormalizeKubernetesObject(t, objectData)
	}

	for _, normalizeF := range object.NormalizeFuncs {
		objectData = normalizeF(t, objectData)
	}

	return objectData
}
