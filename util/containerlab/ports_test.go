package containerlab_test

import (
	"testing"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
)

func TestProcessPortDefinition(t *testing.T) {
	cases := []struct {
		name             string
		portDefinition   string
		expectedProtocol string
		expectedPort     int64
	}{
		{
			name:             "bare-port-defaults-to-tcp",
			portDefinition:   "22",
			expectedProtocol: clabernetesconstants.TCP,
			expectedPort:     22,
		},
		{
			name:             "lower-case-protocol",
			portDefinition:   "5201/udp",
			expectedProtocol: clabernetesconstants.UDP,
			expectedPort:     5201,
		},
		{
			name:             "upper-case-protocol",
			portDefinition:   "57400/TCP",
			expectedProtocol: clabernetesconstants.TCP,
			expectedPort:     57400,
		},
		{
			name:             "max-port",
			portDefinition:   "65535",
			expectedProtocol: clabernetesconstants.TCP,
			expectedPort:     65535,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, err := clabernetesutilcontainerlab.ProcessPortDefinition(
				testCase.portDefinition,
			)
			if err != nil {
				t.Fatalf("expected no error, got %s", err)
			}

			if actual.Protocol != testCase.expectedProtocol {
				t.Fatalf(
					"expected protocol %q, got %q",
					testCase.expectedProtocol,
					actual.Protocol,
				)
			}

			if actual.DestinationPort != testCase.expectedPort {
				t.Fatalf(
					"expected destination port %d, got %d",
					testCase.expectedPort,
					actual.DestinationPort,
				)
			}

			if actual.ExposePort != 0 {
				t.Fatalf("expected no expose port allocation, got %d", actual.ExposePort)
			}
		})
	}
}

// TestProcessPortDefinitionErrors covers the forms the previous (unanchored regex) parser read
// silently and wrongly: a host ip binding became port 4, a range collapsed to its bounds, and an
// unknown protocol was downgraded to tcp.
func TestProcessPortDefinitionErrors(t *testing.T) {
	cases := []struct {
		name           string
		portDefinition string
	}{
		{name: "host-ip-binding", portDefinition: "1.2.3.4:80:80"},
		{name: "port-range", portDefinition: "50000-50010:50000-50010"},
		{name: "unsupported-protocol", portDefinition: "22:22/sctp"},
		{name: "two-sided-binding", portDefinition: "21022:22"},
		{name: "two-sided-binding-with-protocol", portDefinition: "21022:22/tcp"},
		{name: "bare-range", portDefinition: "50000-50010"},
		{name: "zero-port", portDefinition: "0"},
		{name: "out-of-range-port", portDefinition: "99999"},
		{name: "empty", portDefinition: ""},
		{name: "not-a-port", portDefinition: "ssh"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, err := clabernetesutilcontainerlab.ProcessPortDefinition(
				testCase.portDefinition,
			)
			if err == nil {
				t.Fatalf("expected an error, got port %+v", actual)
			}
		})
	}
}
