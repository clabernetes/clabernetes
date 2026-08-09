package config_test

import (
	"testing"

	clabernetesconfig "github.com/clabernetes/clabernetes/config"
	"github.com/google/go-cmp/cmp"
)

func TestGetNodeSelectorsByName(t *testing.T) {
	cases := []struct {
		name              string
		imageName         string
		selectorsByImage  map[string]map[string]string
		expectedSelectors map[string]string
	}{
		{
			name:      "positive_match",
			imageName: "ghcr.io/nokia/srlinux:24.0.1",
			selectorsByImage: map[string]map[string]string{
				"internal.io/nokia_sros*": {"node-flavour": "baremetal"},
				"ghcr.io/nokia/srlinux*":  {"node-flavour": "amd64"},
				"default":                 {"node-flavour": "cheap"},
			},
			expectedSelectors: map[string]string{
				"node-flavour": "amd64",
			},
		},
		{
			name:      "multiple_positive_match",
			imageName: "ghcr.io/nokia/srlinux:24.0.1",
			selectorsByImage: map[string]map[string]string{
				"internal.io/nokia_sros*":   {"node-flavour": "baremetal"},
				"ghcr.io/nokia/srlinux*":    {"node-flavour": "amd64"},
				"ghcr.io/nokia/srlinux:24*": {"node-flavour": "ARM64"},
				"default":                   {"node-flavour": "cheap"},
			},
			expectedSelectors: map[string]string{
				"node-flavour": "ARM64",
			},
		},
		{
			name:      "default_fallback",
			imageName: "internal.stuff/fancy_image",
			selectorsByImage: map[string]map[string]string{
				"internal.io/nokia_sros*": {"node-flavour": "baremetal"},
				"ghcr.io/nokia/srlinux*":  {"node-flavour": "amd64"},
				"default":                 {"node-flavour": "cheap"},
			},
			expectedSelectors: map[string]string{
				"node-flavour": "cheap",
			},
		},
		{
			name:      "no_match_no_default",
			imageName: "internal.stuff/fancy_image",
			selectorsByImage: map[string]map[string]string{
				"internal.io/nokia_sros*": {"node-flavour": "baremetal"},
				"ghcr.io/nokia/srlinux*":  {"node-flavour": "amd64"},
			},
			expectedSelectors: map[string]string{},
		},
		{
			name:      "empty_image_name_no_default",
			imageName: "",
			selectorsByImage: map[string]map[string]string{
				"internal.io/nokia_sros*": {"node-flavour": "baremetal"},
				"ghcr.io/nokia/srlinux*":  {"node-flavour": "amd64"},
			},
			expectedSelectors: map[string]string{},
		},
		{
			name:      "empty_image_name_with_default",
			imageName: "",
			selectorsByImage: map[string]map[string]string{
				"internal.io/nokia_sros*": {"node-flavour": "baremetal"},
				"ghcr.io/nokia/srlinux*":  {"node-flavour": "amd64"},
				"default":                 {"node-flavour": "cheapest"},
			},
			expectedSelectors: map[string]string{
				"node-flavour": "cheapest",
			},
		},
		{
			name:              "empty_image_name_and_empty_selectors_config",
			imageName:         "",
			selectorsByImage:  map[string]map[string]string{},
			expectedSelectors: map[string]string{},
		},
	}

	for _, testCase := range cases {
		t.Run(
			testCase.name,
			func(t *testing.T) {
				t.Logf("%s: starting", testCase.name)
				result := clabernetesconfig.GetNodeSelectorsByImage(
					testCase.imageName,
					testCase.selectorsByImage,
				)

				if diff := cmp.Diff(testCase.expectedSelectors, result); diff != "" {
					t.Errorf("mismatch (-want +got):\n%s", diff)
				}
			})
	}
}
