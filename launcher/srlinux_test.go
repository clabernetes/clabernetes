package launcher //nolint:testpackage // tests cover unexported SR Linux runtime helpers

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	claberneteslogging "github.com/clabernetes/clabernetes/logging"
)

type scriptedCommandRunner struct {
	calls [][]string
	runFn func([]string, int) ([]byte, error)
}

func (r *scriptedCommandRunner) run(_ context.Context, args ...string) ([]byte, error) {
	call := append([]string(nil), args...)
	r.calls = append(r.calls, call)

	if r.runFn == nil {
		return nil, nil
	}

	return r.runFn(call, len(r.calls))
}

func TestInspectDockerContainers(t *testing.T) {
	t.Parallel()

	const inspection = `[
		{
			"Id": "container-id",
			"Config": {
				"Labels": {
					"clab-node-kind": "srl",
					"clab-node-name": "srl1"
				}
			},
			"HostConfig": {
				"NetworkMode": "default"
			},
			"NetworkSettings": {
				"Networks": {
					"clab": {
						"Gateway": "172.20.20.1",
						"IPAddress": "172.20.20.2"
					}
				}
			}
		}
	]`

	runner := &scriptedCommandRunner{
		runFn: func(_ []string, _ int) ([]byte, error) {
			return []byte(inspection), nil
		},
	}

	got, err := inspectDockerContainers(
		context.Background(),
		runner,
		[]string{"container-id"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].ID != "container-id" {
		t.Fatalf("unexpected inspection result: %+v", got)
	}

	wantCommand := []string{"docker", "inspect", "--type", "container", "container-id"}
	if !reflect.DeepEqual(runner.calls[0], wantCommand) {
		t.Fatalf("inspection command = %v, want %v", runner.calls[0], wantCommand)
	}
}

func TestSelectSRLinuxContainers(t *testing.T) {
	t.Parallel()

	containers := []dockerContainerInspection{
		{
			ID: "srl-id",
			Config: struct {
				Labels map[string]string `json:"Labels"`
			}{
				Labels: map[string]string{
					containerlabNodeKindLabel: "srl",
					containerlabNodeNameLabel: "srl1",
				},
			},
			HostConfig: struct {
				NetworkMode string `json:"NetworkMode"`
			}{NetworkMode: "default"},
			NetworkSettings: struct {
				Networks map[string]dockerNetworkInspection `json:"Networks"`
			}{
				Networks: map[string]dockerNetworkInspection{
					"clab": {Gateway: "172.20.20.1", IPAddress: "172.20.20.2"},
				},
			},
		},
		{
			ID: "linux-id",
			Config: struct {
				Labels map[string]string `json:"Labels"`
			}{
				Labels: map[string]string{
					containerlabNodeKindLabel: "linux",
					containerlabNodeNameLabel: "tool",
				},
			},
		},
		{
			ID: "grouped-id",
			Config: struct {
				Labels map[string]string `json:"Labels"`
			}{
				Labels: map[string]string{
					containerlabNodeKindLabel: "nokia_srlinux",
					containerlabNodeNameLabel: "secondary",
				},
			},
			HostConfig: struct {
				NetworkMode string `json:"NetworkMode"`
			}{NetworkMode: "container:primary"},
		},
	}

	got, err := selectSRLinuxContainers(containers, "clab")
	if err != nil {
		t.Fatal(err)
	}

	want := []srlinuxContainer{{
		id:      "srl-id",
		name:    "srl1",
		gateway: "172.20.20.1",
		ip:      "172.20.20.2",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selected containers = %+v, want %+v", got, want)
	}
}

func TestSelectSRLinuxContainersRejectsMissingNetworkData(t *testing.T) {
	t.Parallel()

	container := dockerContainerInspection{
		ID: "srl-id",
		Config: struct {
			Labels map[string]string `json:"Labels"`
		}{
			Labels: map[string]string{
				containerlabNodeKindLabel: "nokia_srlinux",
				containerlabNodeNameLabel: "srl1",
			},
		},
		HostConfig: struct {
			NetworkMode string `json:"NetworkMode"`
		}{NetworkMode: "default"},
		NetworkSettings: struct {
			Networks map[string]dockerNetworkInspection `json:"Networks"`
		}{
			Networks: map[string]dockerNetworkInspection{
				"clab": {IPAddress: "172.20.20.2"},
			},
		},
	}

	_, err := selectSRLinuxContainers([]dockerContainerInspection{container}, "clab")
	if err == nil || !strings.Contains(err.Error(), "has no gateway") {
		t.Fatalf("error = %v, want missing gateway error", err)
	}
}

func TestWaitForSRLinuxInterfacesRetries(t *testing.T) {
	t.Parallel()

	attempts := 0
	runner := &scriptedCommandRunner{
		runFn: func(_ []string, _ int) ([]byte, error) {
			attempts++
			if attempts < 4 {
				return nil, context.DeadlineExceeded
			}

			return nil, nil
		},
	}

	err := waitForSRLinuxInterfaces(
		context.Background(),
		runner,
		testSRLinuxContainer(),
		0,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(runner.calls) != 7 {
		t.Fatalf("interface checks = %d, want 7", len(runner.calls))
	}
}

func TestWaitForSRLinuxInterfacesTimesOut(t *testing.T) {
	t.Parallel()

	runner := &scriptedCommandRunner{
		runFn: func(_ []string, _ int) ([]byte, error) {
			return nil, context.DeadlineExceeded
		},
	}

	err := waitForSRLinuxInterfaces(
		context.Background(),
		runner,
		testSRLinuxContainer(),
		0,
		time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timeout error", err)
	}
}

func TestApplySRLinuxForwardingCommandsAndVerification(t *testing.T) {
	t.Parallel()

	runner := &scriptedCommandRunner{
		runFn: func(args []string, _ int) ([]byte, error) {
			if strings.Contains(strings.Join(args, " "), "route show dev mgmt0-0") {
				return []byte(`[{"dst":"172.20.20.2","dev":"mgmt0-0"}]`), nil
			}

			if strings.Contains(strings.Join(args, " "), "route get 172.20.20.1") {
				return []byte(`[{"dst":"172.20.20.1","dev":"mgmt0"}]`), nil
			}

			if strings.Contains(strings.Join(args, " "), "route show dev mgmt0") {
				return []byte(`[
					{"dst":"172.20.20.1","dev":"mgmt0"},
					{"dst":"default","gateway":"172.20.20.1","dev":"mgmt0"}
				]`), nil
			}

			return nil, nil
		},
	}

	err := applySRLinuxForwarding(
		context.Background(),
		runner,
		testSRLinuxContainer(),
		0,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(runner.calls) != 11 {
		t.Fatalf("commands = %d, want 11", len(runner.calls))
	}

	wantRoute := []string{
		"docker", "exec", "container-id", "ip", "route", "replace", "172.20.20.1",
		"dev", "mgmt0", "scope", "link",
	}
	if !reflect.DeepEqual(runner.calls[4], wantRoute) {
		t.Fatalf("gateway route command = %v, want %v", runner.calls[4], wantRoute)
	}
}

func TestApplySRLinuxForwardingIsIdempotent(t *testing.T) {
	t.Parallel()

	runner := &scriptedCommandRunner{
		runFn: func(args []string, _ int) ([]byte, error) {
			if strings.Contains(strings.Join(args, " "), "route show dev mgmt0-0") {
				return []byte(`[{"dst":"172.20.20.2","dev":"mgmt0-0"}]`), nil
			}

			if strings.Contains(strings.Join(args, " "), "route get 172.20.20.1") {
				return []byte(`[{"dst":"172.20.20.1","dev":"mgmt0"}]`), nil
			}

			if strings.Contains(strings.Join(args, " "), "route show dev mgmt0") {
				return []byte(`[
					{"dst":"172.20.20.1","dev":"mgmt0"},
					{"dst":"default","gateway":"172.20.20.1","dev":"mgmt0"}
				]`), nil
			}

			return nil, nil
		},
	}

	for range 2 {
		err := applySRLinuxForwarding(
			context.Background(),
			runner,
			testSRLinuxContainer(),
			0,
			time.Second,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestApplySRLinuxForwardingStopsOnCommandFailure(t *testing.T) {
	t.Parallel()

	runner := &scriptedCommandRunner{
		runFn: func(_ []string, call int) ([]byte, error) {
			if call == 5 {
				return nil, context.Canceled
			}

			return nil, nil
		},
	}

	err := applySRLinuxForwarding(
		context.Background(),
		runner,
		testSRLinuxContainer(),
		0,
		time.Second,
	)
	if err == nil || !strings.Contains(err.Error(), "configuring SR Linux node") {
		t.Fatalf("error = %v, want command failure", err)
	}
}

func TestNodeReadinessRequiresSRLinuxForwarding(t *testing.T) {
	t.Parallel()

	clabernetes := &clabernetes{
		logger: &claberneteslogging.FakeInstance{},
	}

	if clabernetes.getNodeReadiness(&statusProbeConfiguration{}) {
		t.Fatal("node was ready before SR Linux forwarding completed")
	}

	clabernetes.srLinuxForwardingReady = true

	if !clabernetes.getNodeReadiness(&statusProbeConfiguration{}) {
		t.Fatal("node was not ready after SR Linux forwarding completed")
	}
}

func testSRLinuxContainer() srlinuxContainer {
	return srlinuxContainer{
		id:      "container-id",
		name:    "srl1",
		gateway: "172.20.20.1",
		ip:      "172.20.20.2",
	}
}
