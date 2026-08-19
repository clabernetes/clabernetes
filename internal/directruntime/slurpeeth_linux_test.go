//go:build linux

//nolint:gocyclo,noinlineerr,testpackage,wsl_v5 // Namespace/process tests are explicit.
package directruntime

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/carlmontanari/slurpeeth/slurpeeth"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const (
	slurpeethFakeProcessChild = "C9S_SLURPEETH_FAKE_PROCESS_CHILD"
	slurpeethDaemonChild      = "C9S_SLURPEETH_DAEMON_CHILD"
	slurpeethNamespaceChild   = "C9S_SLURPEETH_NAMESPACE_TEST_CHILD"
	slurpeethTestConfig       = "C9S_SLURPEETH_TEST_CONFIG"
	slurpeethTestReady        = "C9S_SLURPEETH_TEST_READY"
	slurpeethTestPending      = "C9S_SLURPEETH_TEST_PENDING"
)

type synchronizedBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	return b.buffer.Write(value)
}

func (b *synchronizedBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	return b.buffer.String()
}

func TestProcessSlurpeethRuntimeReplacesAndStopsOwnedChild(t *testing.T) {
	if os.Getenv(slurpeethFakeProcessChild) == "1" {
		runFakeSlurpeethProcessChild(t)

		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	processRuntime := &processSlurpeethRuntime{
		stateDirectory: t.TempDir(),
		errors:         make(chan error, 1),
		newCommand: func(configPath, readyPath string) (*exec.Cmd, error) {
			command := exec.CommandContext(
				t.Context(),
				executable,
				"-test.run=^TestProcessSlurpeethRuntimeReplacesAndStopsOwnedChild$",
			)
			command.Env = append(
				os.Environ(),
				slurpeethFakeProcessChild+"=1",
				slurpeethTestConfig+"="+configPath,
				slurpeethTestReady+"="+readyPath,
			)

			return command, nil
		},
	}
	t.Cleanup(func() {
		_ = processRuntime.Close()
	})
	segments := []SlurpeethSegment{{
		Owner: "c9s:direct:v1:pod:slurpeeth:link:nodes",
		ID:    73, Interface: "c9ssowned00001", Destination: "127.0.0.1",
	}}
	if err = processRuntime.Reconcile(t.Context(), segments); err != nil {
		t.Fatal(err)
	}
	processRuntime.mu.Lock()
	first := processRuntime.child
	processRuntime.mu.Unlock()
	if first == nil {
		t.Fatal("slurpeeth runtime did not retain its child identity")
	}
	firstPID := first.command.Process.Pid
	if err = processRuntime.Reconcile(t.Context(), segments); err != nil {
		t.Fatal(err)
	}
	processRuntime.mu.Lock()
	unchanged := processRuntime.child
	processRuntime.mu.Unlock()
	if unchanged == nil || unchanged.command.Process.Pid != firstPID {
		t.Fatalf("unchanged config replaced process %d", firstPID)
	}
	segments[0].ID = 81
	if err = processRuntime.Reconcile(t.Context(), segments); err != nil {
		t.Fatal(err)
	}
	processRuntime.mu.Lock()
	replaced := processRuntime.child
	processRuntime.mu.Unlock()
	if replaced == nil || replaced.command.Process.Pid == firstPID {
		t.Fatalf("changed config retained process %d", firstPID)
	}
	select {
	case <-first.done:
	case <-time.After(time.Second):
		t.Fatal("replaced slurpeeth child did not stop")
	}
	if err = processRuntime.Reconcile(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-replaced.done:
	case <-time.After(time.Second):
		t.Fatal("removed slurpeeth child did not stop")
	}
	processRuntime.mu.Lock()
	remaining := processRuntime.child
	processRuntime.mu.Unlock()
	if remaining != nil {
		t.Fatalf("empty config retained slurpeeth child PID %d", remaining.command.Process.Pid)
	}
	select {
	case processErr := <-processRuntime.errors:
		t.Fatalf("expected process replacement was reported as failure: %v", processErr)
	default:
	}
}

func TestProcessSlurpeethRuntimeKeepsListenerWhilePeerIsPending(t *testing.T) {
	if os.Getenv(slurpeethFakeProcessChild) == "1" {
		runFakeSlurpeethProcessChild(t)

		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	processRuntime := &processSlurpeethRuntime{
		stateDirectory: t.TempDir(),
		errors:         make(chan error, 1),
		newCommand: func(configPath, readyPath string) (*exec.Cmd, error) {
			command := exec.CommandContext(
				t.Context(),
				executable,
				"-test.run=^TestProcessSlurpeethRuntimeKeepsListenerWhilePeerIsPending$",
			)
			command.Env = append(
				os.Environ(),
				slurpeethFakeProcessChild+"=1",
				slurpeethTestConfig+"="+configPath,
				slurpeethTestReady+"="+readyPath,
				slurpeethTestPending+"=1",
			)

			return command, nil
		},
	}
	t.Cleanup(func() {
		_ = processRuntime.Close()
	})
	segments := []SlurpeethSegment{{
		Owner: "c9s:direct:v1:pod:slurpeeth:pending",
		ID:    73, Interface: "c9ssowned00001", Destination: "192.0.2.17",
	}}
	started := time.Now()
	if err = processRuntime.Reconcile(t.Context(), segments); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("pending peer blocked slurpeeth process reconciliation")
	}
	deadline := time.Now().Add(time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp", "127.0.0.1:4799", 25*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()

			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending peer did not retain listening process: %v", dialErr)
		}
		time.Sleep(time.Millisecond)
	}
	ready, err := processRuntime.Ready()
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("pending slurpeeth peer reported ready")
	}
}

func runFakeSlurpeethProcessChild(t *testing.T) {
	t.Helper()
	configPath := os.Getenv(slurpeethTestConfig)
	readyPath := os.Getenv(slurpeethTestReady)
	raw, err := os.ReadFile(filepath.Clean(configPath))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:4799")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = listener.Close()
	}()
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = connection.Close()
		}
	}()
	if os.Getenv(slurpeethTestPending) != "1" {
		if err = writePrivateFileAtomically(
			readyPath,
			[]byte(clabernetesinternaldeviceplan.Digest(raw)+"\n"),
		); err != nil {
			t.Fatal(err)
		}
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
}

func TestSlurpeethDataPlaneAcrossIsolatedNamespaces(t *testing.T) {
	if os.Getenv(slurpeethNamespaceChild) == "1" {
		testSlurpeethDataPlaneAcrossNamespaces(t)

		return
	}
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Skip("unshare is unavailable")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext( //nolint:gosec // The resolved unshare wraps this test binary.
		t.Context(),
		unshare,
		"-Urn",
		executable,
		"-test.run=^TestSlurpeethDataPlaneAcrossIsolatedNamespaces$",
	)
	command.Env = append(os.Environ(), slurpeethNamespaceChild+"=1")
	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "Operation not permitted") {
			t.Skip("unprivileged user/network namespaces are unavailable")
		}
		t.Fatalf("isolated slurpeeth dataplane test failed: %v\n%s", err, output)
	}
}

func TestSlurpeethDaemonChild(t *testing.T) {
	if os.Getenv(slurpeethDaemonChild) != "1" {
		t.Skip("slurpeeth daemon child entrypoint")
	}
	if err := RunSlurpeethDaemon(
		os.Getenv(slurpeethTestConfig),
		os.Getenv(slurpeethTestReady),
	); err != nil {
		t.Fatal(err)
	}
}

func testSlurpeethDataPlaneAcrossNamespaces(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	leftNamespace, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = leftNamespace.Close()
	}()
	rightNamespace, err := netns.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = rightNamespace.Close()
	}()
	if err = netns.Set(leftNamespace); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = netns.Set(leftNamespace)
	}()

	attributes := netlink.NewLinkAttrs()
	attributes.Name = "underlay-a"
	underlay := netlink.NewVeth(attributes)
	underlay.PeerName = "underlay-b"
	if err = netlink.LinkAdd(underlay); err != nil {
		t.Fatal(err)
	}
	rightUnderlay, err := netlink.LinkByName("underlay-b")
	if err != nil {
		t.Fatal(err)
	}
	if err = netlink.LinkSetNsFd(rightUnderlay, int(rightNamespace)); err != nil {
		t.Fatal(err)
	}
	configureTestLink(t, "lo", "")
	configureTestLink(t, "underlay-a", "172.31.0.1/24")
	leftOperations := netlinkOperations{}
	if err = leftOperations.EnsureVethPair(
		"app-a",
		"slurp-a",
		1450,
		"c9s:direct:v1:left:slurpeeth:wire",
	); err != nil {
		t.Fatal(err)
	}
	configureTestLink(t, "app-a", "198.18.0.1/24")

	if err = netns.Set(rightNamespace); err != nil {
		t.Fatal(err)
	}
	configureTestLink(t, "lo", "")
	configureTestLink(t, "underlay-b", "172.31.0.2/24")
	rightOperations := netlinkOperations{}
	if err = rightOperations.EnsureVethPair(
		"app-b",
		"slurp-b",
		1450,
		"c9s:direct:v1:right:slurpeeth:wire",
	); err != nil {
		t.Fatal(err)
	}
	configureTestLink(t, "app-b", "198.18.0.2/24")

	testDirectory := t.TempDir()
	leftConfig := filepath.Join(testDirectory, "left.yaml")
	rightConfig := filepath.Join(testDirectory, "right.yaml")
	leftReady := filepath.Join(testDirectory, "left.ready")
	rightReady := filepath.Join(testDirectory, "right.ready")
	leftRaw, leftDigest, err := renderSlurpeethConfig([]SlurpeethSegment{{
		Owner: "c9s:direct:v1:left:slurpeeth:wire",
		ID:    91, Interface: "slurp-a", Destination: "172.31.0.2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	rightRaw, rightDigest, err := renderSlurpeethConfig([]SlurpeethSegment{{
		Owner: "c9s:direct:v1:right:slurpeeth:wire",
		ID:    91, Interface: "slurp-b", Destination: "172.31.0.1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = writePrivateFileAtomically(leftConfig, leftRaw); err != nil {
		t.Fatal(err)
	}
	if err = writePrivateFileAtomically(rightConfig, rightRaw); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	leftCommand, leftOutput := startSlurpeethTestDaemon(
		t,
		executable,
		leftNamespace,
		leftConfig,
		leftReady,
	)
	rightCommand, rightOutput := startSlurpeethTestDaemon(
		t,
		executable,
		rightNamespace,
		rightConfig,
		rightReady,
	)
	defer func() {
		stopSlurpeethTestDaemon(t, rightCommand, rightOutput)
	}()
	defer func() {
		stopSlurpeethTestDaemon(t, leftCommand, leftOutput)
	}()
	waitForSlurpeethTestMarker(t, leftReady, leftDigest, leftCommand, leftOutput)
	waitForSlurpeethTestMarker(t, rightReady, rightDigest, rightCommand, rightOutput)

	ping, err := exec.LookPath("ping")
	if err != nil {
		t.Skip("ping is unavailable")
	}
	if err = netns.Set(rightNamespace); err != nil {
		t.Fatal(err)
	}
	requireSlurpeethTestPing(t, ping, "198.18.0.1", leftOutput, rightOutput)

	stopSlurpeethTestDaemon(t, leftCommand, leftOutput)
	for range 3 {
		command := exec.CommandContext( //nolint:gosec // Fixed ping diagnostic in an isolated ns.
			t.Context(),
			ping,
			"-c",
			"1",
			"-W",
			"1",
			"198.18.0.1",
		)
		_, _ = command.CombinedOutput()
	}
	waitForSlurpeethTestMarkerRemoval(t, rightReady, rightCommand, rightOutput)
	leftCommand, leftOutput = startSlurpeethTestDaemon(
		t,
		executable,
		leftNamespace,
		leftConfig,
		leftReady,
	)
	if err = netns.Set(rightNamespace); err != nil {
		t.Fatal(err)
	}
	waitForSlurpeethTestMarker(t, leftReady, leftDigest, leftCommand, leftOutput)
	waitForSlurpeethTestMarker(t, rightReady, rightDigest, rightCommand, rightOutput)
	requireSlurpeethTestPing(t, ping, "198.18.0.1", leftOutput, rightOutput)
}

func requireSlurpeethTestPing(
	t *testing.T,
	ping,
	destination string,
	leftOutput,
	rightOutput *synchronizedBuffer,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var output []byte
	var err error
	for {
		command := exec.CommandContext( //nolint:gosec // Fixed ping diagnostic in an isolated ns.
			t.Context(),
			ping,
			"-c",
			"1",
			"-W",
			"1",
			destination,
		)
		output, err = command.CombinedOutput()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"slurpeeth dataplane ping failed: %v\n%s\nleft:\n%s\nright:\n%s",
				err,
				output,
				leftOutput.String(),
				rightOutput.String(),
			)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForSlurpeethTestMarkerRemoval(
	t *testing.T,
	path string,
	command *exec.Cmd,
	output *synchronizedBuffer,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := os.Stat(filepath.Clean(path))
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if command.ProcessState != nil && command.ProcessState.Exited() {
			t.Fatalf("slurpeeth daemon exited during peer loss:\n%s", output.String())
		}
		if time.Now().After(deadline) {
			t.Fatalf("slurpeeth daemon retained readiness after peer loss:\n%s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func startSlurpeethTestDaemon(
	t *testing.T,
	executable string,
	namespace netns.NsHandle,
	configPath,
	readyPath string,
) (*exec.Cmd, *synchronizedBuffer) {
	t.Helper()
	if err := netns.Set(namespace); err != nil {
		t.Fatal(err)
	}
	output := &synchronizedBuffer{}
	command := exec.CommandContext(
		t.Context(),
		executable,
		"-test.run=^TestSlurpeethDaemonChild$",
	)
	command.Env = append(
		os.Environ(),
		slurpeethDaemonChild+"=1",
		slurpeethTestConfig+"="+configPath,
		slurpeethTestReady+"="+readyPath,
	)
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}

	return command, output
}

func waitForSlurpeethTestMarker(
	t *testing.T,
	path,
	digest string,
	command *exec.Cmd,
	output *synchronizedBuffer,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := os.ReadFile(filepath.Clean(path))
		if err == nil && strings.TrimSpace(string(raw)) == digest {
			return
		}
		if command.ProcessState != nil && command.ProcessState.Exited() {
			t.Fatalf("slurpeeth daemon exited before readiness:\n%s", output.String())
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("slurpeeth daemon did not become ready:\n%s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func stopSlurpeethTestDaemon(t *testing.T, command *exec.Cmd, output *synchronizedBuffer) {
	t.Helper()
	if command.ProcessState != nil && command.ProcessState.Exited() {
		return
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil &&
		!errors.Is(err, os.ErrProcessDone) {
		t.Errorf("signaling slurpeeth daemon: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("waiting for slurpeeth daemon: %v\n%s", err, output.String())
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Errorf("slurpeeth daemon did not stop:\n%s", output.String())
	}
}

func TestRenderSlurpeethConfigIsDeterministicAndContainsNoKindIdentity(t *testing.T) {
	segments := []SlurpeethSegment{
		{
			Owner: "c9s:direct:v1:pod:slurpeeth:link-b:nodes",
			ID:    82, Interface: "c9ssbbbbbbbb", Destination: "192.0.2.82",
		},
		{
			Owner: "c9s:direct:v1:pod:slurpeeth:link-a:nodes",
			ID:    81, Interface: "c9ssaaaaaaaa", Destination: "192.0.2.81",
		},
	}
	normalized, err := normalizeSlurpeethSegments(segments)
	if err != nil {
		t.Fatal(err)
	}
	first, firstDigest, err := renderSlurpeethConfig(normalized)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err = normalizeSlurpeethSegments([]SlurpeethSegment{segments[1], segments[0]})
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := renderSlurpeethConfig(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || firstDigest != secondDigest {
		t.Fatalf("slurpeeth config is not deterministic:\n%s\n%s", first, second)
	}
	if strings.Contains(string(first), "package-kind") {
		t.Fatalf("slurpeeth config contains kind identity:\n%s", first)
	}
}

func TestReadDirectSlurpeethMessageHandlesFragmentedTCPReads(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})
	want := slurpeeth.NewMessageFromBody(73, "0123456789", []byte("ethernet-frame"))
	written := make(chan error, 1)
	go func() {
		for _, value := range want.Output() {
			if _, err := left.Write([]byte{value}); err != nil {
				written <- err

				return
			}
		}
		written <- nil
	}()
	got, err := readDirectSlurpeethMessage(right)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-written; err != nil {
		t.Fatal(err)
	}
	if got.Header.ID != want.Header.ID || got.Header.Sender != want.Header.Sender ||
		!bytes.Equal(got.Body, want.Body) {
		t.Fatalf("fragmented message differs: %#v", got)
	}
}

func ExampleSlurpeethSegment() {
	segment := SlurpeethSegment{ID: 73, Interface: "c9ss01234567890", Destination: "192.0.2.17"}
	fmt.Printf("%d %s %s", segment.ID, segment.Interface, segment.Destination)
	// Output: 73 c9ss01234567890 192.0.2.17
}
