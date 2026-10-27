package direct_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	clabernetestesthelper "github.com/clabernetes/clabernetes/testhelper"
)

const labRolePortForwardTimeout = time.Minute

var labRolePortForwardAddress = regexp.MustCompile(`Forwarding from (127\.0\.0\.1:\d+)`)

// TestLabEditorRoleIsNamespaceScoped proves the chart's clabernetes-lab-editor ClusterRole, bound
// in one namespace, lets a ServiceAccount operate a lab there -- stop, start, and restart a Node,
// exec, port-forward, and read logs -- while it cannot touch Deployments or Pods directly, read
// Secrets, or reach another namespace.
func TestLabEditorRoleIsNamespaceScoped(t *testing.T) {
	t.Parallel()

	testName := "lab-editor-role"
	namespace := clabernetestesthelper.NewTestNamespace(testName)
	other := clabernetestesthelper.NewTestNamespace(testName + "-other")

	for _, name := range []string{namespace, other} {
		clabernetestesthelper.KubectlCreateNamespace(t, name)
	}

	defer func() {
		if t.Failed() {
			clabernetestesthelper.DumpNamespaceDiagnostics(t, namespace)
		}

		if !*clabernetestesthelper.SkipCleanup {
			for _, name := range []string{namespace, other} {
				clabernetestesthelper.KubectlDeleteNamespace(t, name)
			}
		}
	}()

	kubectl(t, "create", "serviceaccount", "lab-user", "--namespace", namespace)
	kubectl(t,
		"create", "rolebinding", "lab-user-editor", "--namespace", namespace,
		"--clusterrole", "clabernetes-lab-editor",
		"--serviceaccount", namespace+":lab-user",
	)

	user := "system:serviceaccount:" + namespace + ":lab-user"

	assertLabEditorAccess(t, user, namespace, other)

	if t.Failed() {
		t.FailNow()
	}

	// The lab is created by the bound user itself.
	asUser := func(arguments ...string) []string {
		return append([]string{"--as", user, "--namespace", namespace}, arguments...)
	}

	applyAs := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(), "kubectl", asUser("apply", "-f", "test-fixtures/30-linux-dataplane.yaml")...,
	)
	clabernetestesthelper.Execute(t, applyAs)

	for _, nodeName := range []string{"lin1", "lin2"} {
		waitForDirectNodeReady(t, namespace, nodeName)
	}

	// Stop, start, and restart through the Node only.
	kubectl(t, asUser("patch", "node.c9s.run", "lin2", "--type", "merge",
		"-p", `{"spec":{"runState":"Stopped"}}`)...)
	waitForNodeCondition(t, namespace, "lin2", "Stopped", "True")
	kubectl(t, asUser("patch", "node.c9s.run", "lin2", "--type", "merge",
		"-p", `{"spec":{"runState":"Running"}}`)...)
	waitForDirectNodeReady(t, namespace, "lin2")

	lin2 := observeDevicePod(t, namespace, "lin2")
	kubectl(t, asUser("annotate", "node.c9s.run", "lin2", "--overwrite",
		"c9s.run/restart-requested=lab-editor-1")...)
	waitForDevicePodReplacement(t, namespace, "lin2", lin2.podName)
	waitForDirectNodeReady(t, namespace, "lin2")

	// Exec, logs, and port-forward into the device container.
	lin1 := observeDevicePod(t, namespace, "lin1")
	output := kubectl(t, asUser("exec", lin1.podName, "-c", lin1.containerName, "--",
		"ping", "-c", "2", "-W", "2", "192.168.1.1")...)
	if !strings.Contains(output, " 0% packet loss") {
		t.Fatalf("exec as lab editor failed to reach the peer: %s", output)
	}

	kubectl(t, asUser("logs", lin1.podName, "-c", lin1.containerName, "--tail", "1")...)
	assertPortForward(t, asUser("port-forward", "pod/"+lin1.podName, ":80"))

	// Workloads stay controller-owned, and other namespaces stay out of reach.
	for _, arguments := range [][]string{
		asUser("scale", "deployment", "lin1", "--replicas", "0"),
		asUser("delete", "pod", lin1.podName),
		asUser("get", "secrets"),
		{"--as", user, "--namespace", other, "get", "nodes.c9s.run"},
	} {
		forbidden := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
			t.Context(), "kubectl", arguments...,
		)

		out, err := forbidden.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "forbidden") {
			t.Errorf("kubectl %v as lab editor was not forbidden: %v: %s", arguments, err, out)
		}
	}
}

// assertLabEditorAccess checks what the editor role allows in its namespace and denies beyond it.
func assertLabEditorAccess(t *testing.T, user, namespace, other string) {
	t.Helper()

	for _, check := range []struct {
		verb, resource, subresource, namespace string
		allowed                                bool
	}{
		{"get", "topologies.c9s.run", "", namespace, true},
		{"create", "topologies.c9s.run", "", namespace, true},
		{"patch", "nodes.c9s.run", "", namespace, true},
		{"delete", "links.c9s.run", "", namespace, true},
		{"watch", "nodeprofiles.c9s.run", "", namespace, true},
		{"create", "configmaps", "", namespace, true},
		{"list", "persistentvolumeclaims", "", namespace, true},
		{"delete", "persistentvolumeclaims", "", namespace, true},
		{"create", "persistentvolumeclaims", "", namespace, false},
		{"delete", "persistentvolumeclaims", "", other, false},
		{"list", "services", "", namespace, true},
		{"list", "events", "", namespace, true},
		{"get", "pods", "log", namespace, true},
		{"create", "pods", "exec", namespace, true},
		{"get", "pods", "exec", namespace, true},
		{"create", "pods", "portforward", namespace, true},
		{"update", "deployments.apps", "", namespace, false},
		{"patch", "deployments.apps", "", namespace, false},
		{"create", "pods", "", namespace, false},
		{"delete", "pods", "", namespace, false},
		{"create", "pods", "attach", namespace, false},
		{"get", "secrets", "", namespace, false},
		{"create", "rolebindings.rbac.authorization.k8s.io", "", namespace, false},
		{"create", "namespaces", "", "", false},
		{"get", "namespaces/" + namespace, "", "", true},
		{"get", "namespaces/" + other, "", "", false},
		{"list", "namespaces", "", "", false},
		{"patch", "nodes.c9s.run", "", other, false},
		{"get", "pods", "", other, false},
		{"create", "pods", "exec", other, false},
	} {
		arguments := []string{"auth", "can-i", check.verb, check.resource, "--as", user}
		if check.subresource != "" {
			arguments = append(arguments, "--subresource", check.subresource)
		}

		if check.namespace != "" {
			arguments = append(arguments, "--namespace", check.namespace)
		}

		answer := kubectlAnswer(t, arguments...)
		if want := map[bool]string{true: "yes", false: "no"}[check.allowed]; answer != want {
			t.Errorf(
				"can-i %s %s/%s in %q = %q, want %q",
				check.verb, check.resource, check.subresource, check.namespace, answer, want,
			)
		}
	}

	// metrics.k8s.io is usually not served on test clusters, which kubectl auth can-i needs for
	// resource discovery; authorization itself does not, so ask it directly.
	if allowed := subjectAccessReview(t, user, namespace, "metrics.k8s.io", "pods", "list"); !allowed {
		t.Error("lab editor cannot list Pod metrics")
	}
}

// subjectAccessReview asks the authorizer whether user may perform verb on a resource.
func subjectAccessReview(t *testing.T, user, namespace, group, resource, verb string) bool {
	t.Helper()

	review := fmt.Sprintf(`{"apiVersion":"authorization.k8s.io/v1","kind":"SubjectAccessReview",`+
		`"spec":{"user":%q,"resourceAttributes":{"namespace":%q,"group":%q,"resource":%q,`+
		`"verb":%q}}}`, user, namespace, group, resource, verb)

	cmd := exec.CommandContext(
		t.Context(), "kubectl", "create", "-f", "-", "-o", "jsonpath={.status.allowed}",
	)
	cmd.Stdin = strings.NewReader(review)

	return strings.TrimSpace(string(clabernetestesthelper.Execute(t, cmd))) == "true"
}

func kubectl(t *testing.T, arguments ...string) string {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(), "kubectl", arguments...,
	)

	return string(clabernetestesthelper.Execute(t, cmd))
}

// kubectlAnswer runs a command whose non-zero exit carries an answer, like auth can-i.
func kubectlAnswer(t *testing.T, arguments ...string) string {
	t.Helper()

	cmd := exec.CommandContext( //nolint:gosec // kubectl arguments are test-controlled.
		t.Context(), "kubectl", arguments...,
	)
	output, _ := cmd.Output()

	return strings.TrimSpace(string(output))
}

// assertPortForward starts kubectl port-forward and proves an HTTP request reaches the device.
func assertPortForward(t *testing.T, arguments []string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), labRolePortForwardTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", arguments...) //nolint:gosec // test-controlled.

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}

	defer func() {
		cancel()

		_ = cmd.Wait()
	}()

	address := ""
	scanner := bufio.NewScanner(stdout)

	for scanner.Scan() {
		if match := labRolePortForwardAddress.FindStringSubmatch(scanner.Text()); match != nil {
			address = match[1]

			break
		}
	}

	if address == "" {
		t.Fatal("port-forward as lab editor never listened")
	}

	go func() { _, _ = io.Copy(io.Discard, stdout) }()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request through port-forward as lab editor failed: %v", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("request through port-forward returned %s", response.Status)
	}
}
