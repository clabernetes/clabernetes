package kubernetes_test

import (
	"strings"
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutilkubernetes "github.com/clabernetes/clabernetes/util/kubernetes"
)

func TestSafeConcatNameKubernetes(t *testing.T) {
	cases := []struct {
		name     string
		in       []string
		expected string
	}{
		{
			name:     "simple",
			in:       []string{"afinename"},
			expected: "afinename",
		},
		{
			name:     "simple-multi-word",
			in:       []string{"a", "fine", "name"},
			expected: "a-fine-name",
		},
		{
			name: "over-max-len",
			in: []string{
				"a",
				"fine",
				"name",
				"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			},
			expected: "a-fine-name-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx-8fa96d7",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutilkubernetes.SafeConcatNameKubernetes(testCase.in...)
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestSafeConcatNameMax(t *testing.T) {
	cases := []struct {
		name     string
		in       []string
		max      int
		expected string
	}{
		{
			name:     "simple",
			in:       []string{"afinename"},
			max:      30,
			expected: "afinename",
		},
		{
			name:     "simple-multi-word",
			in:       []string{"a", "fine", "name"},
			max:      30,
			expected: "a-fine-name",
		},
		{
			name: "over-max-len",
			in: []string{
				"a",
				"fine",
				"name",
				"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			},
			max:      30,
			expected: "a-fine-name-xxxxxxxxxx-8fa96d7",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutilkubernetes.SafeConcatNameMax(testCase.in, testCase.max)
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestEnforceDNSLabelConvention(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		expected string
	}{
		{
			name:     "simple",
			in:       "afinename",
			expected: "afinename",
		},
		{
			name:     "ending-with-non-alpha",
			in:       "afinename1",
			expected: "afinenamez",
		},
		{
			name:     "starting-with-non-alpha",
			in:       "1afinename",
			expected: "zafinename",
		},
		{
			name:     "special-chars",
			in:       "afine.name",
			expected: "afine-name",
		},
		{
			name:     "ending-starting-with-non-alpha-special-chars",
			in:       "1afine.name2",
			expected: "zafine-namez",
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutilkubernetes.EnforceDNSLabelConvention(testCase.in)
				if actual != testCase.expected {
					clabernetestesthelper.FailOutput(t, actual, testCase.expected)
				}
			})
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"r1":            "r1",
		"R1":            "r1",
		"PE_1":          "pe-1",
		"Spine-01":      "spine-01",
		"node one":      "node-one",
		"7750-SR":       "clab-7750-sr",
		"-leaf-":        "leaf",
		"srv6+flexalgo": "srv6-flexalgo",
		"___":           "",
	}

	for name, want := range cases {
		got := clabernetesutilkubernetes.SanitizeName(name)
		if got != want {
			t.Fatalf("SanitizeName(%q) = %q, want %q", name, got, want)
		}

		if again := clabernetesutilkubernetes.SanitizeName(got); again != got {
			t.Fatalf("SanitizeName(%q) = %q, want the sanitized name to be stable", got, again)
		}
	}
}

func TestSanitizeNameTruncatesToADNSLabel(t *testing.T) {
	long := strings.Repeat("Router", 20)

	got := clabernetesutilkubernetes.SanitizeName(long)
	if len(got) != clabernetesutilkubernetes.NameMaxLen {
		t.Fatalf("len(SanitizeName(long)) = %d, want %d",
			len(got), clabernetesutilkubernetes.NameMaxLen)
	}

	if again := clabernetesutilkubernetes.SanitizeName(got); again != got {
		t.Fatalf("SanitizeName(%q) = %q, want the sanitized name to be stable", got, again)
	}

	if other := clabernetesutilkubernetes.SanitizeName(long + "X"); other == got {
		t.Fatal("truncated names of different nodes must not collapse onto one name")
	}
}
