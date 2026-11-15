// Copyright 2020 Nokia
// Licensed under the BSD 3-Clause License.
// SPDX-License-Identifier: BSD-3-Clause

package compiler

import (
	"fmt"
	"strings"

	clabernetesutilcontainerlab "github.com/clabernetes/clabernetes/util/containerlab"
	yamlv3 "gopkg.in/yaml.v3"
)

const (
	linkAddressIPv4Field   = "ipv4"
	linkAddressIPv6Field   = "ipv6"
	invalidLinkAddressCode = "invalid-link-address"
)

// linkDefinition represents a link definition in the topology file. It accepts both
// containerlab's brief "node:interface" endpoint syntax and the equivalent structured
// node/interface syntax used by explicit veth links.
type linkDefinition struct {
	linkConfig `yaml:",inline"`

	Type string `yaml:"type,omitempty"`
}

// linkConfig is the link config object the compiler reads.
type linkConfig struct {
	Endpoints linkEndpoints
	Labels    map[string]string `yaml:"labels,omitempty"`
	Vars      map[string]any    `yaml:"vars,omitempty"`
	MTU       int               `yaml:"mtu,omitempty"`
	IPv4      []string          `yaml:"ipv4,omitempty"`
	IPv6      []string          `yaml:"ipv6,omitempty"`
}

// linkEndpoints stores the complete per-endpoint vocabulary the c9s Link API can represent.
type linkEndpoints []Endpoint

func (e *linkEndpoints) UnmarshalYAML(value *yamlv3.Node) error {
	if value.Kind != yamlv3.SequenceNode {
		return fmt.Errorf("%w: link endpoints must be a sequence", errParse)
	}

	endpoints := make(linkEndpoints, 0, len(value.Content))

	for _, endpointNode := range value.Content {
		switch endpointNode.Kind {
		case yamlv3.ScalarNode:
			endpoint, err := canonicalLinkEndpoint(endpointNode.Value)
			if err != nil {
				return err
			}

			endpoints = append(endpoints, endpoint)
		case yamlv3.MappingNode:
			endpoint := struct {
				Node      string `yaml:"node"`
				Interface string `yaml:"interface"`
				IPv4      string `yaml:"ipv4,omitempty"`
				IPv6      string `yaml:"ipv6,omitempty"`
			}{}

			err := endpointNode.Decode(&endpoint)
			if err != nil {
				return fmt.Errorf(
					"%w: decoding structured link endpoint: %w",
					errParse,
					err,
				)
			}

			err = validateStructuredLinkEndpoint(endpointNode)
			if err != nil {
				return err
			}

			canonical, err := canonicalLinkEndpoint(
				fmt.Sprintf("%s:%s", endpoint.Node, endpoint.Interface),
			)
			if err != nil {
				return err
			}

			canonical.IPv4 = endpoint.IPv4
			canonical.IPv6 = endpoint.IPv6

			endpoints = append(endpoints, canonical)
		case yamlv3.DocumentNode, yamlv3.SequenceNode, yamlv3.AliasNode:
			return fmt.Errorf(
				"%w: link endpoint must be a string or node/interface mapping",
				errParse,
			)
		}
	}

	*e = endpoints

	return nil
}

func canonicalLinkEndpoint(value string) (Endpoint, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != linkEndpointElementCount {
		return Endpoint{}, fmt.Errorf(
			"%w: link endpoint %q must use node:interface syntax",
			errParse,
			value,
		)
	}

	nodeName := strings.TrimSpace(parts[0])

	interfaceName := strings.TrimSpace(parts[1])
	if nodeName == "" || interfaceName == "" {
		return Endpoint{}, fmt.Errorf(
			"%w: link endpoint %q requires non-empty node and interface",
			errParse,
			value,
		)
	}

	return Endpoint{NodeName: nodeName, InterfaceName: interfaceName}, nil
}

func validateStructuredLinkEndpoint(value *yamlv3.Node) error {
	for index := 0; index+1 < len(value.Content); index += 2 {
		key := value.Content[index]
		field := value.Content[index+1]

		switch key.Value {
		case "node", "interface", linkAddressIPv4Field, linkAddressIPv6Field:
		default:
			return fmt.Errorf(
				"%w: structured link endpoint field %q is not supported by clabernetes",
				errParse,
				key.Value,
			)
		}

		if field.Kind != yamlv3.ScalarNode || field.Tag != "!!str" {
			return fmt.Errorf(
				"%w: structured link endpoint field %q must be a string",
				errParse,
				key.Value,
			)
		}
	}

	return nil
}

// compileContainerlabLinks converts the containerlab links section into compiled wires.
func compileContainerlabLinks(
	fileTopology *fileTopology,
	diagnostics *compileDiagnostics,
) ([]CompiledLink, error) {
	links := make([]CompiledLink, 0, len(fileTopology.Links))

	for linkIndex, link := range fileTopology.Links {
		linkPath := fmt.Sprintf("topology.links[%d]", linkIndex)
		if len(link.Labels) != 0 {
			diagnostics.add(Diagnostic{
				Code:    "unsupported-link-labels",
				Path:    linkPath + ".labels",
				Message: "link labels are not preserved by the c9s Link API",
			})
		}

		if len(link.Vars) != 0 {
			diagnostics.add(Diagnostic{
				Code:    "unsupported-link-vars",
				Path:    linkPath + ".vars",
				Message: "link vars are not preserved by the c9s Link API",
			})
		}

		switch link.Type {
		case "", "brief", "veth":
		default:
			diagnostics.add(unsupportedContainerlabLinkTypeDiagnostic(link.Type, linkPath))

			continue
		}

		if len(link.Endpoints) != linkEndpointElementCount {
			return nil, fmt.Errorf(
				"%w: endpoint '%v' has wrong syntax, unexpected number of items",
				errParse,
				link.Endpoints,
			)
		}

		invalidEndpoint := applyLinkAddressArrays(link, linkPath, diagnostics)

		for endpointIndex := range link.Endpoints {
			nodeName := link.Endpoints[endpointIndex].NodeName

			if nodeName == LinkHostNodeName {
				continue
			}

			if nodeName == "mgmt-net" || nodeName == "macvlan" {
				diagnostics.add(Diagnostic{
					Code: "unsupported-special-endpoint",
					Path: fmt.Sprintf("%s.endpoints[%d]", linkPath, endpointIndex),
					Message: fmt.Sprintf(
						"special endpoint %q requires host networking that c9s does not provide",
						nodeName,
					),
				})

				invalidEndpoint = true

				continue
			}

			if _, exists := fileTopology.Nodes[nodeName]; !exists {
				diagnostics.add(Diagnostic{
					Code:    "unknown-link-endpoint",
					Path:    fmt.Sprintf("%s.endpoints[%d]", linkPath, endpointIndex),
					Message: fmt.Sprintf("link endpoint references nonexistent node %q", nodeName),
				})

				invalidEndpoint = true
			}
		}

		if link.Endpoints[0].NodeName == LinkHostNodeName &&
			link.Endpoints[1].NodeName == LinkHostNodeName {
			diagnostics.add(Diagnostic{
				Code:    "invalid-host-link",
				Path:    linkPath + ".endpoints",
				Message: "a c9s host link must have exactly one Node endpoint",
			})

			invalidEndpoint = true
		}

		if validateLinkEndpointAddresses(link, linkPath, diagnostics) {
			invalidEndpoint = true
		}

		if invalidEndpoint {
			continue
		}

		links = append(links, CompiledLink{
			EndpointA: link.Endpoints[0],
			EndpointB: link.Endpoints[1],
			MTU:       link.MTU,
		})
	}

	return links, nil
}

func validateLinkEndpointAddresses(
	link *linkDefinition,
	linkPath string,
	diagnostics *compileDiagnostics,
) bool {
	invalid := false

	for endpointIndex := range link.Endpoints {
		for _, address := range []struct {
			field string
			value string
			ipv4  bool
		}{
			{field: linkAddressIPv4Field, value: link.Endpoints[endpointIndex].IPv4, ipv4: true},
			{field: linkAddressIPv6Field, value: link.Endpoints[endpointIndex].IPv6},
		} {
			if address.value == "" {
				continue
			}
			path := fmt.Sprintf("%s.endpoints[%d].%s", linkPath, endpointIndex, address.field)
			if link.Endpoints[endpointIndex].NodeName == LinkHostNodeName {
				diagnostics.add(invalidLinkAddressDiagnostic(
					path,
					"endpoint addresses require a Node endpoint",
				))
				invalid = true

				continue
			}
			if _, err := clabernetesutilcontainerlab.NormalizeLinkEndpointAddress(
				address.value,
				address.ipv4,
			); err != nil {
				diagnostics.add(invalidLinkAddressDiagnostic(path, err.Error()))
				invalid = true
			}
		}
	}

	return invalid
}

func invalidLinkAddressDiagnostic(path, message string) Diagnostic {
	return Diagnostic{Code: invalidLinkAddressCode, Path: path, Message: message}
}

// applyLinkAddressArrays maps link-level address entries onto their endpoints. It reports whether
// the Link became invalid and leaves family/CIDR validation to the common endpoint pass below.
func applyLinkAddressArrays(
	link *linkDefinition,
	linkPath string,
	diagnostics *compileDiagnostics,
) bool {
	invalid := false

	for _, family := range []struct {
		field  string
		values []string
		ipv4   bool
	}{
		{field: linkAddressIPv4Field, values: link.IPv4, ipv4: true},
		{field: linkAddressIPv6Field, values: link.IPv6},
	} {
		if len(family.values) > linkEndpointElementCount {
			diagnostics.add(invalidLinkAddressDiagnostic(
				linkPath+"."+family.field,
				"link address arrays must contain at most two entries",
			))
			invalid = true
		}

		for endpointIndex, value := range family.values {
			if value == "" || endpointIndex >= linkEndpointElementCount {
				continue
			}
			endpoint := &link.Endpoints[endpointIndex]
			current := &endpoint.IPv4
			if !family.ipv4 {
				current = &endpoint.IPv6
			}
			if *current != "" && *current != value {
				diagnostics.add(invalidLinkAddressDiagnostic(
					fmt.Sprintf(
						"%s.%s[%d]",
						linkPath,
						family.field,
						endpointIndex,
					),
					"link-level and structured endpoint addresses disagree",
				))
				invalid = true

				continue
			}
			*current = value
		}
	}

	return invalid
}

func unsupportedContainerlabLinkTypeDiagnostic(linkType, linkPath string) Diagnostic {
	message := fmt.Sprintf(
		"native link type %q has no c9s topology-link equivalent",
		linkType,
	)

	if linkType == "host" {
		message = fmt.Sprintf(
			"explicit native link type %q requires structured endpoints that the "+
				"c9s topology compiler does not support; use brief endpoints",
			linkType,
		)
	}

	return Diagnostic{
		Code:    "unsupported-link-type",
		Path:    linkPath + ".type",
		Message: message,
	}
}
