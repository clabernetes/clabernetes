package util_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

func TestIsURL(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		expected bool
	}{
		{
			name:     "http",
			in:       "http://blah.com",
			expected: true,
		},
		{
			name:     "https",
			in:       "https://blah.com",
			expected: true,
		},
		{
			name:     "file",
			in:       "/some/file",
			expected: false,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutil.IsURL(testCase.in)

				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestWriteHTTPContentsFromPath(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{
			name:    "simple",
			headers: map[string]string{"foo": "bar"},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				fakeServer := httptest.NewServer(http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						for k, v := range testCase.headers {
							actual := r.Header.Get(k)
							if actual != v {
								clabernetestesthelper.FailOutput(t, actual, v)
							}
						}

						// just write a dummy message, we're just making sure the client sets
						// headers and actually makes an http call
						_, _ = fmt.Fprintf(w, "foo")
					},
				))
				defer fakeServer.Close()

				b := make([]byte, 100)
				w := bytes.NewBuffer(b)

				err := clabernetesutil.WriteHTTPContentsFromPath(
					t.Context(),
					fakeServer.URL,
					w,
					testCase.headers,
				)
				if err != nil {
					t.Fatalf("unexpected error: %s", err)
				}

				if !strings.Contains(w.String(), "foo") {
					t.Fatal("writer did not contain expected content 'foo'")
				}
			})
	}
}

func TestGitHubNormalLinkToRawLink(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		expected string
	}{
		{
			name:     "not-a-github-link",
			in:       "http://blah.com",
			expected: "http://blah.com",
		},
		{
			name:     "github-normal-link",
			in:       "https://github.com/srl-labs/srl-telemetry-lab/blob/main/st.clab.yml",
			expected: "https://raw.githubusercontent.com/srl-labs/srl-telemetry-lab/main/st.clab.yml",
		},
		{
			name:     "github-already-raw-link",
			in:       "https://raw.githubusercontent.com/srl-labs/srl-telemetry-lab/main/st.clab.yml",
			expected: "https://raw.githubusercontent.com/srl-labs/srl-telemetry-lab/main/st.clab.yml",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutil.GitHubNormalToRawLink(testCase.in)
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestGitHubGroupAndRepoFromURL(t *testing.T) {
	cases := []struct {
		name          string
		in            string
		expectedGroup string
		expectedRepo  string
	}{
		{
			name:          "not-a-github-link",
			in:            "http://blah.com",
			expectedGroup: "",
			expectedRepo:  "",
		},
		{
			name:          "clabernetes",
			in:            "https://github.com/clabernetes/clabernetes",
			expectedGroup: "clabernetes",
			expectedRepo:  "clabernetes",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actualGroup, actualRepo := clabernetesutil.GitHubGroupAndRepoFromURL(testCase.in)
				if actualGroup != testCase.expectedGroup {
					clabernetestesthelper.FailOutput(t, actualGroup, testCase.expectedGroup)
				}

				if actualRepo != testCase.expectedRepo {
					clabernetestesthelper.FailOutput(t, actualRepo, testCase.expectedRepo)
				}
			})
	}
}
