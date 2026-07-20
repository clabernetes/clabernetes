package testhelper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
)

// ReadTestFixtureFile is a helper to read a test fixture file.
func ReadTestFixtureFile(t *testing.T, f string) []byte { //nolint:thelper
	return ReadTestFile(t, filepath.Join("test-fixtures", f))
}

// ReadTestFile is a helper to read a (usually) golden file in the context of a test
// (hence testing.T).
func ReadTestFile(t *testing.T, f string) []byte {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(wd, f)) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}

	return content
}

// WriteTestFixtureFile is a helper to write out a test fixture file.
func WriteTestFixtureFile(t *testing.T, f string, b []byte) { //nolint:thelper
	WriteTestFile(t, filepath.Join("test-fixtures", f), b)
}

// WriteTestFile is a helper to write json to the specified file in the context of a test.
func WriteTestFile(t *testing.T, f string, b []byte) {
	t.Helper()

	err := os.WriteFile(f, b, clabernetesconstants.PermissionsEveryoneReadWriteOwnerExecute)
	if err != nil {
		t.Fatal(err)
	}
}

// WriteTestFixtureJSON is a helper to write JSON into a test fixture file.
func WriteTestFixtureJSON(t *testing.T, f string, o any) { //nolint:thelper
	WriteTestJSON(t, filepath.Join("test-fixtures", f), o)
}

// WriteTestJSON is a helper to write JSON to the specified file in the context of a test.
func WriteTestJSON(t *testing.T, f string, o any) {
	t.Helper()

	j, err := json.MarshalIndent(o, "", "    ")
	if err != nil {
		t.Fatal(err)
	}

	WriteTestFile(t, f, j)
}
