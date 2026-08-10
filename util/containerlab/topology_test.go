package containerlab_test

import (
	"slices"
	"strings"
	"testing"

	clabernetesutil "github.com/clabernetes/clabernetes/util"
	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

func TestLoadContainerlabConfigFromString(t *testing.T) {
	cases := []struct {
		config string
	}{
		{
			config: `
name: topo01

topology:
  nodes:
    srl1:
      kind: srl
      image: ghcr.io/nokia/srlinux
`,
		},
		{
			config: `
name: topo02

topology:
  nodes:
    srl2:
      kind: srl
      image: ghcr.io/nokia/srlinux
      ports:
        - 5201/udp
      devices:
        - /dev/net/tun
      cap-add:
        - NET_ADMIN
      privileged: true
      shm-size: 256m
      tmpfs:
        /tmp/scratch: size=64m
      security-opts:
        - seccomp=unconfined
      certificate:
        issue: true
        key-size: 4096
        validity-duration: 8760h
        sans:
          - srl2.example.com
`,
		},
	}

	for _, testCase := range cases {
		_, unknownFields, err := clabernetesutilcontainerlab.LoadContainerlabConfig(
			testCase.config,
		)
		if err != nil {
			t.Errorf("Unable to load containerlab config: %s", err)
		}

		if len(unknownFields) != 0 {
			t.Errorf("Config uses only known vocabulary but got warnings: %q", unknownFields)
		}
	}
}

func TestLoadContainerlabConfigFromConfigObjects(t *testing.T) {
	cases := []struct {
		config *clabernetesutilcontainerlab.Config
	}{
		{
			config: getMinimalValidConfigObject(),
		},
		{
			config: getFullVocabularyConfigObject(),
		},
	}

	for _, testCase := range cases {
		marshaled, err := yaml.Marshal(testCase.config)
		if err != nil {
			t.Fatalf("Marshal error: %v", err)
		}

		cfg, unknownFields, err := clabernetesutilcontainerlab.LoadContainerlabConfig(
			string(marshaled),
		)
		if err != nil {
			t.Errorf("Unable to load containerlab config: %s", err)
		}

		// the config objects are built from the vocabulary types themselves, so a warning here
		// means clabernetes renders a field it cannot read back
		if len(unknownFields) != 0 {
			t.Errorf("Round tripping our own vocabulary produced warnings: %q", unknownFields)
		}

		if diff := cmp.Diff(testCase.config.Topology, cfg.Topology); diff != "" {
			t.Errorf("Configs not equal (-got +want):\n%s", diff)
		}
	}
}

// TestLoadContainerlabConfigWarnsOnUnknownFields covers the asymmetry between the two ways a
// containerlab topology reaches clabernetes: a Node custom resource is validated strictly by the
// apiserver, but a Topology definition is native containerlab text, so vocabulary clabernetes has
// no home for is dropped with a warning rather than failing the lab.
func TestLoadContainerlabConfigWarnsOnUnknownFields(t *testing.T) {
	// publish and stages are real containerlab vocabulary clabernetes does not implement,
	// tooootally-not-a-field stands in for a typo or a newer containerlab
	config, unknownFields, err := clabernetesutilcontainerlab.LoadContainerlabConfig(`
name: topo01
topology:
  nodes:
    srl1:
      kind: srl
      image: ghcr.io/nokia/srlinux
      publish:
        - tcp/22
      tooootally-not-a-field: 1
      stages:
        create:
          wait-for:
            - node: srl2
    srl2:
      kind: srl
  links:
    - endpoints: ["srl1:e1-1", "srl2:e1-1"]
`)
	if err != nil {
		t.Fatalf("unknown fields must not fail the load, got error: %s", err)
	}

	// the known vocabulary around the unknown fields still has to land
	if len(config.Topology.Nodes) != 2 || len(config.Topology.Links) != 1 {
		t.Errorf(
			"unknown fields cost us known vocabulary: %d nodes, %d links",
			len(config.Topology.Nodes),
			len(config.Topology.Links),
		)
	}

	if config.Topology.Nodes["srl1"].Image != "ghcr.io/nokia/srlinux" {
		t.Errorf("expected srl1 image to survive, got %q", config.Topology.Nodes["srl1"].Image)
	}

	for _, field := range []string{"publish", "tooootally-not-a-field", "stages"} {
		if !slices.ContainsFunc(unknownFields, func(warning string) bool {
			return strings.Contains(warning, field)
		}) {
			t.Errorf("expected a warning naming %q, got %q", field, unknownFields)
		}
	}

	if len(unknownFields) != 3 {
		t.Errorf("expected exactly 3 warnings, got %q", unknownFields)
	}
}

// TestLoadContainerlabConfigRejectsBadValues is the other half of the asymmetry: leniency covers
// fields clabernetes does not know, never a known field holding an unusable value. Dropping one of
// those would silently change the lab the user asked for.
func TestLoadContainerlabConfigRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"wrong type for a known field": `
name: topo01
topology:
  nodes:
    srl1:
      kind: srl
      binds: not-a-list
`,
		"wrong type alongside an unknown field": `
name: topo01
topology:
  nodes:
    srl1:
      kind: srl
      publish: [tcp/22]
      binds: not-a-list
`,
		"malformed yaml": `
name: topo01
topology:
   nodes:
  srl1: {
`,
		// every caller walks Topology, so this must be an error rather than a nil deref
		"no topology section": "name: topo01\n",
		"empty definition":    "",
	}

	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := clabernetesutilcontainerlab.LoadContainerlabConfig(config)
			if err == nil {
				t.Error("expected an error, got none")
			}
		})
	}
}

// getFullVocabularyConfigObject exercises a node definition populated across the whole curated
// vocabulary, so the yaml round trip covers every sub object rather than just the scalars.
func getFullVocabularyConfigObject() *clabernetesutilcontainerlab.Config {
	config := &clabernetesutilcontainerlab.Config{Name: "fullVocabularyConfig"}
	config.Topology = &clabernetesutilcontainerlab.Topology{
		Defaults: &clabernetesutilcontainerlab.NodeDefinition{Ports: []string{}},
		Nodes:    make(map[string]*clabernetesutilcontainerlab.NodeDefinition),
	}
	node := &clabernetesutilcontainerlab.NodeDefinition{
		Kind:                  "srl",
		Type:                  "ixr-d3",
		Image:                 "ghcr.io/nokia/srlinux",
		License:               "/opt/license.key",
		StartupConfig:         "/opt/startup.cfg",
		EnforceStartupConfig:  clabernetesutil.ToPointer(true),
		SuppressStartupConfig: clabernetesutil.ToPointer(false),
		Entrypoint:            "/entrypoint.sh",
		Cmd:                   "--verbose",
		Exec:                  []string{"ip link set dev e1-1 up"},
		User:                  "root",
		Binds:                 []string{"/tmp/foo:/tmp/foo"},
		Devices:               []string{"/dev/net/tun"},
		CapAdd:                []string{"NET_ADMIN"},
		Privileged:            clabernetesutil.ToPointer(true),
		SecurityOpts:          []string{"seccomp=unconfined"},
		Tmpfs:                 map[string]string{"/tmp/scratch": "size=64m"},
		ShmSize:               "256m",
		Ports:                 []string{"5201/udp"},
		MgmtIPv4:              "172.20.20.5",
		MgmtIPv6:              "3fff:172:20:20::5",
		NetworkMode:           "container:srl0",
		Env:                   map[string]string{"FOO": "bar"},
		EnvFiles:              []string{"/opt/env"},
		Sysctls:               map[string]string{"net.ipv4.ip_forward": "1"},
		DNS: &clabernetesutilcontainerlab.DNSConfig{
			Servers: []string{"1.1.1.1"},
			Options: []string{"ndots:2"},
			Search:  []string{"example.com"},
		},
		Certificate: &clabernetesutilcontainerlab.CertificateConfig{
			Issue:            clabernetesutil.ToPointer(true),
			KeySize:          4096,
			ValidityDuration: "8760h",
			SANs:             []string{"srl1.example.com"},
		},
		Extras: &clabernetesutilcontainerlab.Extras{
			SRLAgents:       []string{"/opt/agent.yml"},
			CeosCopyToFlash: []string{"/opt/flash-me"},
		},
		Components: []*clabernetesutilcontainerlab.Component{
			{
				Slot: "A",
				Type: "imm32-qsfp28+4-qsfpdd",
			},
		},
	}
	config.Topology.Nodes["srl1"] = node

	return config
}

func getMinimalValidConfigObject() *clabernetesutilcontainerlab.Config {
	config := &clabernetesutilcontainerlab.Config{Name: "minimalValidConfig"}
	config.Topology = &clabernetesutilcontainerlab.Topology{
		Defaults: &clabernetesutilcontainerlab.NodeDefinition{Ports: []string{}},
		Nodes:    make(map[string]*clabernetesutilcontainerlab.NodeDefinition),
	}
	node := &clabernetesutilcontainerlab.NodeDefinition{
		Ports: []string{},
		Kind:  "srl",
		Image: "ghcr.io/nokia/srlinux",
	}
	config.Topology.Nodes["srl1"] = node

	return config
}
