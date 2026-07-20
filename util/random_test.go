package util_test

import (
	"testing"
	"unicode"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

const (
	alphabet = "abcdefghijklmnopqrstuvwxyz"
)

func TestRandomString(t *testing.T) {
	cases := []struct {
		name string
		l    int
	}{
		{
			name: "simple",
			l:    1,
		},
		{
			name: "long",
			l:    100,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutil.RandomString(testCase.l)

				if len(actual) != testCase.l {
					clabernetestesthelper.FailOutput(t, len(actual), testCase.l)
				}

				for _, char := range actual {
					if !unicode.IsLetter(char) {
						t.Fatalf("expected only letters in random string, got %s", string(char))
					}
				}
			})
	}
}
