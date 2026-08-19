package deviceplan_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

func TestEveryLiveRegistryNameFlowsThroughGenericPlanningBoundary(t *testing.T) {
	registry := clabernetesdeviceplan.NewContainerlabRegistry()
	compatibility, err := clabernetesdeviceplan.CompatibilityForRegistry(
		registry,
		"test-linked-version",
	)
	if err != nil {
		t.Fatal(err)
	}
	kinds := registry.GetRegisteredNodeKindNames()
	if len(kinds) == 0 {
		t.Fatal("imported containerlab registry is empty")
	}
	for _, kind := range kinds {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			definition, marshalErr := json.Marshal(map[string]string{
				"kind":  kind,
				"image": "registry.invalid/package-conformance:latest",
			})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			input := clabernetesdeviceplan.Input{
				SchemaVersion: clabernetesdeviceplan.SchemaVersion,
				TopologyName:  "registry-conformance",
				Compatibility: compatibility,
				Nodes: []clabernetesdeviceplan.NodeInput{{
					ID: "registry-node", Name: "registry-node", Kind: kind,
					Definition: definition,
				}},
				Management: []clabernetesdeviceplan.ManagementInput{{
					NodeID: "registry-node", IPv4: "192.0.2.10/24",
					IPv4Gateway: "192.0.2.1", IPv6: "2001:db8::10/64",
					IPv6Gateway: "2001:db8::1",
					DNS: clabernetesdeviceplan.DNSConfig{
						Servers: []string{"192.0.2.53", "2001:db8::53"},
						Search:  []string{"registry-conformance.example"},
						Options: []string{"ndots:1"},
					},
				}},
			}
			declared, declaredErr := clabernetesdeviceplan.DiscoverDeclaredImages(
				input,
				"registry-conformance",
			)
			if declaredErr != nil {
				t.Fatalf("discovering declared image: %v", declaredErr)
			}
			if len(declared.Images) != 1 ||
				declared.Images[0].SourceReference != "registry.invalid/package-conformance:latest" {
				t.Fatalf("declared image discovery = %#v", declared.Images)
			}
			for _, requirement := range declared.Images {
				input.Images = append(input.Images, clabernetesdeviceplan.ImageInput{
					NodeID: requirement.NodeID, SourceReference: requirement.SourceReference,
					DigestReference: requirement.SourceReference + "@sha256:" + strings.Repeat(
						"a",
						64,
					),
					Platform: clabernetesdeviceplan.Platform{
						OS:           "linux",
						Architecture: "amd64",
					},
				})
			}
			discovery, discoverErr := (clabernetesdeviceplan.Adapter{
				Registry: registry,
				Revision: "registry-conformance",
			}).DiscoverImages(context.Background(), input)
			if discoverErr != nil {
				var planningErr *clabernetesdeviceplan.Error
				if !errors.As(discoverErr, &planningErr) ||
					planningErr.Code != clabernetesdeviceplan.ErrorInvalidInput ||
					planningErr.Field != "definition" || planningErr.Behavior != "imported-init" {
					t.Fatalf(
						"generic image discovery failed outside imported input validation: %v",
						discoverErr,
					)
				}

				return
			}
			if discovery == nil || discovery.InputDigest == "" {
				t.Fatalf("generic image discovery returned no identity: %#v", discovery)
			}
			planningInput := input
			planningInput.Images = nil
			represented := map[string]bool{}
			for _, requirement := range discovery.Images {
				planningInput.Images = append(
					planningInput.Images,
					conformanceImageInput(requirement),
				)
				represented[requirement.NodeID+"\x00"+requirement.SourceReference] = true
			}
			for _, requirement := range declared.Images {
				if represented[requirement.NodeID+"\x00"+requirement.SourceReference] {
					continue
				}
				image := conformanceImageInput(requirement)
				image.Role = ""
				planningInput.Images = append(planningInput.Images, image)
			}
			certificateInputs, certificateRoot := materializeCertificateRequirements(
				t,
				discovery.Certificates,
			)
			planningInput.Certificates = certificateInputs
			planContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			plan, planErr := (clabernetesdeviceplan.Adapter{
				Registry:        registry,
				Revision:        "registry-conformance",
				CertificateRoot: certificateRoot,
			}).Plan(planContext, planningInput)
			if planErr != nil {
				var planningErr *clabernetesdeviceplan.Error
				if !errors.As(planErr, &planningErr) ||
					(planningErr.Code != clabernetesdeviceplan.ErrorUnsupported &&
						planningErr.Code != clabernetesdeviceplan.ErrorSideEffect) ||
					planningErr.NodeID != input.Nodes[0].ID || planningErr.Field == "" ||
					planningErr.Behavior == "" {
					t.Fatalf("generic planning failed without a capability diagnostic: %v", planErr)
				}
				if cause := errors.Unwrap(planningErr); cause != nil {
					t.Logf("generic planning capability: %v; cause: %v", planErr, cause)
				} else {
					t.Logf("generic planning capability: %v", planErr)
				}

				return
			}
			if plan == nil || len(plan.Nodes) != 1 || len(plan.Containers) == 0 {
				t.Fatalf("generic planning returned no direct application plan: %#v", plan)
			}
			if len(plan.Management) != 1 || plan.Management[0].NodeID != "registry-node" ||
				(plan.Management[0].InterfaceName == "" &&
					plan.Management[0].InterfaceSelector !=
						clabernetesdeviceplan.ManagementInterfacePodTransport) ||
				plan.Management[0].IPv4 != "192.0.2.10/24" ||
				plan.Management[0].IPv6 != "2001:db8::10/64" ||
				!slices.Equal(
					plan.Management[0].DNS.Servers,
					[]string{"192.0.2.53", "2001:db8::53"},
				) {
				t.Fatalf("generic registry management plan = %#v", plan.Management)
			}
		})
	}
}

func conformanceImageInput(
	requirement clabernetesdeviceplan.ImageRequirement,
) clabernetesdeviceplan.ImageInput {
	digestReference := requirement.SourceReference
	if !strings.Contains(digestReference, "@sha256:") {
		digestReference += "@sha256:" + strings.Repeat("a", 64)
	}

	return clabernetesdeviceplan.ImageInput{
		NodeID: requirement.NodeID, Role: requirement.Role,
		SourceReference: requirement.SourceReference, DigestReference: digestReference,
		Platform: clabernetesdeviceplan.Platform{OS: "linux", Architecture: "amd64"},
	}
}
