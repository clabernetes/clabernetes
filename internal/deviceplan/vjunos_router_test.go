package deviceplan_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
)

//nolint:gocyclo // Verifies the imported image, management, startup, and interface contracts together.
func TestBoxenVJunosRouterPlan(t *testing.T) {
	t.Parallel()

	registry := clabernetesinternaldeviceplan.NewContainerlabRegistry()
	input := singleNodeInput("juniper_vjunosrouter", "example/juniper_vjunos-router:25.2R1.9")
	compatibility, err := clabernetesinternaldeviceplan.CompatibilityForRegistry(
		registry,
		"test-linked-version",
	)
	if err != nil {
		t.Fatal(err)
	}
	input.Compatibility = compatibility
	input.Nodes[0].Definition, err = json.Marshal(map[string]any{
		"kind": "juniper_vjunosrouter", "image": input.Images[0].SourceReference,
		"startup-config": "system { domain-name startup.example; }\n",
		"healthcheck": map[string]any{
			"test":     []string{"CMD", "/boxen/boxen", "health"},
			"interval": 5, "timeout": 5, "retries": 1, "start-period": 1200,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Images[0].Config = clabernetesinternaldeviceplan.ImageConfig{
		Entrypoint: []string{"/boxen/boxen", "run"}, WorkingDir: "/boxen",
		Labels: []clabernetesinternaldeviceplan.KeyValue{
			{Name: "org.opencontainers.image.vendor", Value: "Boxen"},
		},
	}
	input.Management = []clabernetesinternaldeviceplan.ManagementInput{{
		NodeID: "node-a", IPv4: "192.0.2.10/24", IPv4Gateway: "192.0.2.1",
		IPv6: "2001:db8::10/64", IPv6Gateway: "2001:db8::1",
	}}
	input.Interfaces = []clabernetesinternaldeviceplan.InterfaceInput{{
		ID: "interface-a", NodeID: "node-a", Name: "ge-0/0/0", LinkID: "link-a",
		Connectivity: clabernetesinternaldeviceplan.ConnectivityWire, WireID: 1,
	}}
	adapter := clabernetesinternaldeviceplan.Adapter{
		Registry: registry,
		Revision: "vjunos-router-test",
	}
	discovery, err := adapter.DiscoverImages(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Certificates, adapter.CertificateRoot = materializeCertificateRequirements(
		t,
		discovery.Certificates,
	)
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Containers) != 1 ||
		!slices.Equal(plan.Containers[0].ImageEntrypoint, []string{"/boxen/boxen", "run"}) {
		t.Fatalf("Boxen container plan = %#v", plan.Containers)
	}
	if !slices.Contains(plan.Containers[0].Command, "--hostname") ||
		!slices.Contains(plan.Containers[0].Command, "router") {
		t.Fatalf("Containerlab run arguments = %#v", plan.Containers[0].Command)
	}
	if health := plan.Containers[0].Healthcheck; health == nil ||
		!slices.Equal(health.Test, []string{"CMD", "/boxen/boxen", "health"}) ||
		health.StartPeriod != int64(20*time.Minute) {
		t.Fatalf("nested Junos startup health check = %#v", health)
	}
	if len(plan.Interfaces) != 1 || plan.Interfaces[0].Name != "eth1" ||
		plan.Interfaces[0].Alias != "ge-0/0/0" ||
		plan.Interfaces[0].LinkApplyMode != clabernetesinternaldeviceplan.LinkApplyLive {
		t.Fatalf("live interface plan = %#v", plan.Interfaces)
	}
	if len(plan.Management) != 1 || plan.Management[0].Interposition == nil ||
		plan.Management[0].Interposition.DeviceInterface != "eth0" ||
		plan.Management[0].IPv4 != input.Management[0].IPv4 ||
		plan.Management[0].IPv6 != input.Management[0].IPv6 {
		t.Fatalf("management plan = %#v", plan.Management)
	}
	if !slices.ContainsFunc(plan.Mounts, func(mount clabernetesinternaldeviceplan.MountPlan) bool {
		return mount.Destination == "/config"
	}) || !slices.ContainsFunc(plan.Files, func(file clabernetesinternaldeviceplan.FilePlan) bool {
		return file.ArtifactPath == "config/startup-config.cfg" &&
			file.Digest == clabernetesinternaldeviceplan.Digest(
				[]byte("system { domain-name startup.example; }\n"),
			)
	}) {
		t.Fatalf(
			"startup configuration not provisioned: mounts=%#v files=%#v",
			plan.Mounts,
			plan.Files,
		)
	}
}
