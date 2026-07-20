package suite

import (
	"regexp"
	"sync"
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

// AssertObject represents an object that we are looking to assert the state of in a test -- the
// simplest form of this would just be the name of an object, but more complicated setups may also
// include custom normalization functions or other helpers.
type AssertObject struct {
	Name                 string
	SkipDefaultNormalize bool
	NormalizeFuncs       []func(t *testing.T, objectData []byte) []byte
}

// Steps is a slice of Step -- used for e2e tests.
type Steps []Step

// Step represents a single step in an e2e test.
type Step struct {
	Index         int
	Description   string
	AssertObjects map[string][]AssertObject
}

type stepPatterns struct {
	stepFixtureType *regexp.Regexp
}

var (
	stepPatternsInstance     *stepPatterns //nolint:gochecknoglobals
	stepPatternsInstanceOnce sync.Once     //nolint:gochecknoglobals
)

func getStepPatterns() *stepPatterns {
	stepPatternsInstanceOnce.Do(func() {
		stepPatternsInstance = &stepPatterns{
			stepFixtureType: regexp.MustCompile(`(?i)\d+-(?P<fixtureType>apply|delete).yaml`),
		}
	})

	return stepPatternsInstance
}

// GetStepFixtureType returns the Operation type of the given test step fixture file.
func GetStepFixtureType(t *testing.T, stepFixtureName string) clabernetestesthelper.Operation {
	t.Helper()

	patterns := getStepPatterns()

	matches := patterns.stepFixtureType.FindStringSubmatch(stepFixtureName)
	opIndex := patterns.stepFixtureType.SubexpIndex("fixtureType")

	resolved := clabernetestesthelper.Operation(matches[opIndex])

	switch resolved {
	case clabernetestesthelper.Apply,
		clabernetestesthelper.Delete,
		clabernetestesthelper.Create,
		clabernetestesthelper.Get:
	default:
		t.Fatalf("fixture type '%s' invalid", resolved)
	}

	return resolved
}
