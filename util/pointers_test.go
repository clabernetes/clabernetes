package util_test

import (
	"testing"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

func TestToPointer(t *testing.T) {
	trueObj := true
	strObj := "hihi"
	i32Obj := int32(10)
	i64Obj := int64(10)

	cases := []struct {
		name     string
		in       any
		expected any
	}{
		{
			name: "bool",
			in:   trueObj,
		},
		{
			name: "string",
			in:   strObj,
		},
		{
			name: "i32",
			in:   i32Obj,
		},
		{
			name: "i64",
			in:   i64Obj,
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)

				actual := clabernetesutil.ToPointer(testCase.in)
				if *actual != testCase.in {
					clabernetestesthelper.FailOutput(t, *actual, testCase.in)
				}
			})
	}
}
