package deviceplan_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesdirectruntime "github.com/clabernetes/clabernetes/internal/directruntime"
	clabcert "github.com/srl-labs/containerlab/cert"
	clabexec "github.com/srl-labs/containerlab/exec"
	clablinks "github.com/srl-labs/containerlab/links"
	clabnodes "github.com/srl-labs/containerlab/nodes"
	clabruntime "github.com/srl-labs/containerlab/runtime"
	clabtypes "github.com/srl-labs/containerlab/types"
	"golang.org/x/sys/unix"
)

const syntheticKind = "future-kind"

// syntheticImportedNode models a node implementation that appeared in a newer containerlab
// dependency. It deliberately lives behind the exported containerlab Node interface so the test
// fails if c9s requires a matching kind registration, switch branch, or fixture.
type syntheticImportedNode struct {
	clabnodes.DefaultNode
}

type syntheticLogStreamer struct {
	mu     sync.Mutex
	target string
}

func (s *syntheticLogStreamer) StreamLogs(
	_ context.Context,
	target string,
) (io.ReadCloser, error) {
	s.mu.Lock()
	s.target = target
	s.mu.Unlock()

	return io.NopCloser(strings.NewReader("package-observed-boot\n")), nil
}

func (s *syntheticLogStreamer) Target() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.target
}

func (n *syntheticImportedNode) Init(
	config *clabtypes.NodeConfig,
	options ...clabnodes.NodeOption,
) error {
	n.DefaultNode = *clabnodes.NewDefaultNode(n)
	n.Cfg = config
	for _, option := range options {
		option(n)
	}
	inspect, err := n.Runtime.InspectImage(context.Background(), config.Image)
	if err != nil {
		return err
	}
	if config.NodeType == "metadata-gated-test" && inspect.Config.Labels["required"] != "true" {
		return errors.New("synthetic package requires explicit image labels")
	}

	if config.MgmtIntf == "" {
		config.MgmtIntf = "imported-mgmt"
	}
	config.RestartPolicy = "imported-default"
	if config.NodeType == "renderable-test" || config.NodeType == "symlink-artifact-test" {
		config.RestartPolicy = "always"
	}
	if config.NodeType == "renderable-test" {
		config.CapAdd = append(config.CapAdd, "SYS_ADMIN")
		config.Tmpfs = map[string]string{
			"/run/future-package": "rw,nosuid,nodev,noexec,size=8M",
		}
	}
	if config.NodeType == "certificate-test" {
		issue := true
		config.Certificate.Issue = &issue
		config.Certificate.SANs = append(config.Certificate.SANs, "future-package.example")
	}
	config.Env["IMPORTED_WORKSPACE"] = filepath.Join(config.LabDir, "generated")
	config.Binds = append(
		config.Binds,
		filepath.Join(config.LabDir, "generated")+":/etc/generated:ro",
	)
	generatedDir := filepath.Join(config.LabDir, "generated")
	if err := os.MkdirAll(generatedDir, 0o750); err != nil {
		return err
	}

	if err := os.WriteFile(
		filepath.Join(generatedDir, "imported.conf"),
		[]byte("generated\n"),
		0o640,
	); err != nil {
		return err
	}
	if config.NodeType != "symlink-artifact-test" {
		return nil
	}
	targetDirectory := filepath.Join(generatedDir, "target")
	if err := os.MkdirAll(targetDirectory, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(
		filepath.Join(targetDirectory, "value"),
		[]byte("package-target\n"),
		0o640,
	); err != nil {
		return err
	}

	return os.Symlink("target", filepath.Join(generatedDir, "alias"))
}

func (*syntheticImportedNode) LinkApplyMode(context.Context) clabnodes.LinkApplyMode {
	return clabnodes.LinkApplyModeLive
}

func (n *syntheticImportedNode) IsHealthy(ctx context.Context) (bool, error) {
	if n.Config().NodeType != "readiness-test" {
		return n.DefaultNode.IsHealthy(ctx)
	}

	return n.Config().MgmtIntf == "imported-mgmt" &&
		len(n.GetEndpoints()) == 1 &&
		n.GetEndpoints()[0].GetIfaceName() == "mapped-eth1", nil
}

func (n *syntheticImportedNode) CheckDeploymentConditions(ctx context.Context) error {
	if n.Config().NodeType == "condition-failure-test" {
		return errors.New("synthetic target-worker requirement is absent")
	}

	return n.DefaultNode.CheckDeploymentConditions(ctx)
}

func (n *syntheticImportedNode) Deploy(
	ctx context.Context,
	params *clabnodes.DeployParams,
) error {
	if n.Config().NodeType == "deployment-replay-test" {
		if err := n.DefaultNode.Deploy(ctx, params); err != nil {
			return err
		}
		hostsPath, err := n.Runtime.GetHostsPath(ctx, n.Config().LongName)
		if err != nil {
			return err
		}
		hostsFile, err := os.OpenFile(hostsPath, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err = hostsFile.WriteString("192.0.2.20 replayed-package-host\n"); err != nil {
			_ = hostsFile.Close()

			return err
		}
		if err = hostsFile.Close(); err != nil {
			return err
		}
		result, err := n.Runtime.Exec(
			ctx,
			n.Config().LongName,
			clabexec.NewExecCmdFromSlice([]string{"/bin/false"}),
		)
		if err != nil {
			return err
		}
		if result.GetReturnCode() != 0 {
			return errors.New("deployment operation executed while reconstructing package state")
		}

		return nil
	}
	if n.Config().NodeType == "deployment-operations-test" {
		if err := n.DefaultNode.Deploy(ctx, params); err != nil {
			return err
		}
		if err := n.Runtime.CopyToContainer(
			ctx,
			n.Config().LongName,
			"/etc/imported-deploy.conf",
			filepath.Join(n.Config().LabDir, "generated", "imported.conf"),
		); err != nil {
			return err
		}
		if err := n.Runtime.WriteToStdinNoWait(
			ctx,
			n.Config().LongName,
			[]byte("package-deploy-stdin\n"),
		); err != nil {
			return err
		}

		return n.Runtime.ExecNotWait(
			ctx,
			n.Config().LongName,
			clabexec.NewExecCmdFromSlice([]string{"package-deploy-command", "--apply"}),
		)
	}
	if n.Config().NodeType != "multi-container-test" &&
		n.Config().NodeType != "multi-container-operations-test" &&
		n.Config().NodeType != "component-exec-target-test" {
		return n.DefaultNode.Deploy(ctx, params)
	}

	root := *n.Config()
	root.LongName += "-root"
	root.ShortName += "-root"
	rootID, err := n.Runtime.CreateContainer(ctx, &root)
	if err != nil {
		return err
	}
	if _, err = n.Runtime.StartContainer(ctx, rootID, n); err != nil {
		return err
	}

	component := *n.Config()
	component.LongName += "-component"
	component.ShortName += "-component"
	component.Image = "example/future-component:1"
	component.NetworkMode = "container:" + rootID
	componentID, err := n.Runtime.CreateContainer(ctx, &component)
	if err != nil {
		return err
	}
	if _, err = n.Runtime.StartContainer(ctx, componentID, n); err != nil {
		return err
	}
	if n.Config().NodeType == "multi-container-operations-test" {
		return n.Runtime.ExecNotWait(
			ctx,
			componentID,
			clabexec.NewExecCmdFromSlice([]string{"component-deploy-command"}),
		)
	}

	return nil
}

func (n *syntheticImportedNode) GetContainers(
	ctx context.Context,
) ([]clabruntime.GenericContainer, error) {
	if n.Config().NodeType != "multi-container-test" &&
		n.Config().NodeType != "multi-container-operations-test" &&
		n.Config().NodeType != "component-exec-target-test" {
		return n.DefaultNode.GetContainers(ctx)
	}

	return n.Runtime.ListContainers(ctx, []*clabtypes.GenericFilter{{
		FilterType: "name", Match: n.Config().LongName + "-root",
	}})
}

func (n *syntheticImportedNode) GetContainerName() string {
	if n.Config().NodeType == "component-exec-target-test" {
		return n.Config().LongName + "-component"
	}

	return n.DefaultNode.GetContainerName()
}

func (n *syntheticImportedNode) DeployEndpoints(ctx context.Context) error {
	switch n.Config().NodeType {
	case "postdeploy-test":
		marker := filepath.Join(n.Config().LabDir, "package-deploy-endpoints-ran")
		if err := os.WriteFile(marker, []byte("package-owned"), 0o600); err != nil {
			return err
		}
	case "pre-realized-endpoint-test":
		endpoints := n.GetEndpoints()
		if len(endpoints) != 1 || !endpoints[0].IsRuntimeDiscovered() ||
			endpoints[0].GetLink() == nil || endpoints[0].GetLink().GetMTU() != 9000 {
			return errors.New("package endpoint does not retain pre-realized topology metadata")
		}

		return n.DefaultNode.DeployEndpoints(ctx)
	case "endpoint-namespace-capability-test":
		// Model an imported implementation that treats a missing host/device namespace
		// distinction as an optional warning. The generic c9s boundary must retain the
		// capability failure even though the package hook returns success.
		_, _ = n.Runtime.GetNSPath(ctx, n.Config().LongName)

		return nil
	case "endpoint-application-exec-capability-test":
		// Endpoint hooks execute from the host namespace. An application command must
		// fail by generic capability instead of accidentally running in the helper.
		_, _ = n.Runtime.Exec(
			ctx,
			n.Config().LongName,
			clabexec.NewExecCmdFromSlice([]string{"package-owned-command"}),
		)

		return nil
	}

	return n.DefaultNode.DeployEndpoints(ctx)
}

func (n *syntheticImportedNode) PostDeployEndpoints(ctx context.Context) error {
	if n.Config().NodeType == "postdeploy-test" {
		deployed, err := os.ReadFile(
			filepath.Join(n.Config().LabDir, "package-deploy-endpoints-ran"),
		)
		if err != nil || string(deployed) != "package-owned" {
			return fmt.Errorf("endpoint deployment did not precede post-deployment fixup")
		}

		return os.WriteFile(
			filepath.Join(n.Config().LabDir, "package-post-deploy-endpoints-ran"),
			[]byte("package-owned"),
			0o600,
		)
	}

	return n.DefaultNode.PostDeployEndpoints(ctx)
}

func (n *syntheticImportedNode) PostDeploy(
	ctx context.Context,
	_ *clabnodes.PostDeployParams,
) error {
	if n.Config().NodeType == "log-stream-test" {
		logs, err := n.Runtime.StreamLogs(ctx, n.Config().LongName)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(logs)
		if closeErr := logs.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if string(raw) != "package-observed-boot\n" {
			return fmt.Errorf("package observed unexpected application logs")
		}

		return os.WriteFile(
			filepath.Join(n.Config().LabDir, "package-log-stream-ran"),
			raw,
			0o600,
		)
	}
	if n.Config().NodeType == "postdeploy-test" ||
		n.Config().NodeType == "deployment-replay-test" {
		if status := n.Runtime.GetContainerStatus(ctx, n.Config().LongName); status != clabruntime.Running {
			return errors.New("synthetic package did not observe its running application container")
		}
		marker := filepath.Join(n.Config().LabDir, "package-post-deploy-ran")
		result, err := n.Runtime.Exec(
			ctx,
			n.Config().LongName,
			clabexec.NewExecCmdFromSlice([]string{
				"/bin/sh", "-c", "printf package-owned > " + marker,
			}),
		)
		if err != nil {
			return err
		}
		if result.GetReturnCode() != 0 {
			return errors.New("synthetic package post-deploy command failed")
		}

		return nil
	}
	runtimeID := n.Config().LongName
	if n.Config().NodeType == "multi-container-test" {
		runtimeID += "-root"
	}
	if n.Config().NodeType == "component-exec-target-test" {
		runtimeID += "-component"
	}
	hostsPath, err := n.Runtime.GetHostsPath(ctx, runtimeID)
	if err != nil {
		return err
	}
	hostsFile, err := os.OpenFile(hostsPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = hostsFile.WriteString("192.0.2.10 package-derived-host\n"); err != nil {
		_ = hostsFile.Close()

		return err
	}
	if err = hostsFile.Close(); err != nil {
		return err
	}
	if err := n.Runtime.CopyToContainer(
		ctx,
		runtimeID,
		"/etc/imported-post.conf",
		filepath.Join(n.Config().LabDir, "generated", "imported.conf"),
	); err != nil {
		return err
	}
	if err := n.Runtime.WriteToStdinNoWait(
		ctx,
		runtimeID,
		[]byte("package-derived-stdin\n"),
	); err != nil {
		return err
	}

	return n.Runtime.ExecNotWait(
		ctx,
		runtimeID,
		clabexec.NewExecCmdFromSlice([]string{"imported-post-deploy", "--apply"}),
	)
}

func (n *syntheticImportedNode) SaveConfig(
	ctx context.Context,
) (*clabnodes.SaveConfigResult, error) {
	if n.Config().NodeType != "save-test" {
		return n.DefaultNode.SaveConfig(ctx)
	}
	result, err := n.RunExec(
		ctx,
		clabexec.NewExecCmdFromSlice([]string{
			"/bin/sh", "-c", "printf package-owned-save",
		}),
	)
	if err != nil {
		return nil, err
	}
	if result.GetReturnCode() != 0 {
		return nil, errors.New("synthetic package save command failed")
	}
	configPath := filepath.Join(n.Config().LabDir, "saved.conf")
	if err = os.WriteFile(configPath, result.GetStdOutByteSlice(), 0o600); err != nil {
		return nil, err
	}

	return &clabnodes.SaveConfigResult{ConfigPath: configPath}, nil
}

func (n *syntheticImportedNode) PreDeploy(
	ctx context.Context,
	params *clabnodes.PreDeployParams,
) error {
	if err := n.DefaultNode.PreDeploy(ctx, params); err != nil {
		return err
	}
	if n.Config().NodeType == "predeploy-panic-test" {
		var missing *int

		_ = *missing
	}
	if n.Config().NodeType == "directory-metadata-test" {
		directory := filepath.Join(n.Config().LabDir, "generated", "metadata")
		if err := os.MkdirAll(directory, 0o710); err != nil {
			return err
		}
		if err := os.Chmod(directory, 0o710); err != nil {
			return err
		}

		return unix.Setxattr(
			directory,
			"user.containerlab-package-test",
			[]byte("opaque-package-metadata"),
			0,
		)
	}
	if n.Config().NodeType == "artifact-ownership-test" {
		directory := filepath.Join(n.Config().LabDir, "generated", "owned")
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return err
		}

		return os.Chown(directory, 1234, 2345)
	}
	if n.Config().NodeType == "certificate-test" {
		certificate, err := n.LoadOrGenerateCertificate(params.Cert, params.TopologyName)
		if err != nil {
			return err
		}
		authority, err := params.Cert.LoadCaCert()
		if err != nil {
			return err
		}

		return os.WriteFile(
			filepath.Join(n.Config().LabDir, "generated", "tls.pem"),
			append(
				append(append([]byte{}, certificate.Cert...), certificate.Key...),
				authority.Cert...),
			0o600,
		)
	}
	if n.Config().NodeType != "payload-workspace-test" {
		return nil
	}
	content, err := os.ReadFile(n.Config().StartupConfig)
	if err != nil {
		return err
	}

	return os.WriteFile(
		filepath.Join(n.Config().LabDir, "generated", "payload-derived.conf"),
		append([]byte("derived:"), content...),
		0o600,
	)
}

func TestPackageRequestedCertificatesFlowWithoutKindMapping(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "certificate-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": "certificate-test", "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "certificate-discovery-v1",
	}
	discovery, err := adapter.DiscoverImages(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Certificates) != 1 ||
		!slices.Contains(discovery.Certificates[0].DNSNames, "future-package.example") {
		t.Fatalf("package certificate requirements = %#v", discovery.Certificates)
	}
	requirement := discovery.Certificates[0]
	certificateInputs, root := materializeCertificateRequirements(t, discovery.Certificates)
	input.Certificates = certificateInputs
	certificateKey, privateKeyKey := clabernetesdeviceplan.CertificateMaterialKeys(
		requirement.NodeID,
		requirement.StorageName,
	)
	nodeCertificate, err := os.ReadFile(filepath.Join(root, certificateKey))
	if err != nil {
		t.Fatal(err)
	}
	nodePrivateKey, err := os.ReadFile(filepath.Join(root, privateKeyKey))
	if err != nil {
		t.Fatal(err)
	}
	adapter.CertificateRoot = root
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !containsFileSource(plan.Files, clabernetesdeviceplan.FileSourceCertificate) {
		t.Fatalf("certificate-backed files = %#v", plan.Files)
	}
	canonical, err := plan.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonical), string(nodePrivateKey)) {
		t.Fatal("private key bytes leaked into the canonical plan")
	}
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err = (clabernetesdeviceplan.Preparer{
		Adapter: adapter,
	}).Prepare(context.Background(), input, *plan, artifactRoot); err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(requirement.NodeID),
		"generated",
		"tls.pem",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(staged), string(nodeCertificate)) ||
		!strings.Contains(string(staged), string(nodePrivateKey)) {
		t.Fatal("prepared certificate-backed artifact does not contain accepted material")
	}
}

func TestImageDiscoveryDoesNotInventCertificateRequest(t *testing.T) {
	t.Parallel()

	discovery, err := (clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "certificate-discovery-v1",
	}).DiscoverImages(
		context.Background(),
		singleNodeInput(syntheticKind, "example/future:1"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Certificates) != 0 {
		t.Fatalf("unrequested package certificates = %#v", discovery.Certificates)
	}
}

func TestPackageGeneratedDirectoryMetadataFlowsWithoutKindMapping(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "directory-metadata-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "directory-metadata-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	attributeName := "user.containerlab-package-test"
	attributeValue := []byte("opaque-package-metadata")
	found := false
	for _, file := range plan.Files {
		if file.ArtifactPath != "generated/metadata" ||
			file.ArtifactKind != clabernetesdeviceplan.ArtifactDirectory {
			continue
		}
		found = file.Mode == 0o710 && len(file.ExtendedAttributes) == 1 &&
			file.ExtendedAttributes[0].Name == attributeName &&
			file.ExtendedAttributes[0].Digest == clabernetesdeviceplan.Digest(attributeValue)
	}
	if !found {
		t.Fatalf("package directory metadata is absent from the generic plan: %#v", plan.Files)
	}
	artifactRoot := t.TempDir()
	if err = (clabernetesdeviceplan.Preparer{Adapter: adapter}).Prepare(
		context.Background(),
		input,
		*plan,
		artifactRoot,
	); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
		"generated",
		"metadata",
	)
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0o710 {
		t.Fatalf("prepared directory mode = %v, err=%v", info, err)
	}
	size, err := unix.Getxattr(directory, attributeName, nil)
	if err != nil {
		t.Fatal(err)
	}
	value := make([]byte, size)
	if _, err = unix.Getxattr(directory, attributeName, value); err != nil {
		t.Fatal(err)
	}
	if string(value) != string(attributeValue) {
		t.Fatalf("prepared directory attribute = %q", value)
	}
}

func TestPackageGeneratedOwnershipFlowsWithoutKindMapping(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("generic ownership capture requires the preparation worker's root identity")
	}

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "artifact-ownership-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "artifact-ownership-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range plan.Files {
		if file.ArtifactPath == "generated/owned" &&
			file.ArtifactKind == clabernetesdeviceplan.ArtifactDirectory &&
			file.UID != nil && *file.UID == 1234 && file.GID != nil && *file.GID == 2345 {
			found = true
		}
	}
	if !found {
		t.Fatalf("package ownership is absent from the generic plan: %#v", plan.Files)
	}
	artifactRoot := t.TempDir()
	if err = (clabernetesdeviceplan.Preparer{Adapter: adapter}).Prepare(
		context.Background(),
		input,
		*plan,
		artifactRoot,
	); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
		"generated",
		"owned",
	))
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 1234 || stat.Gid != 2345 {
		t.Fatalf("prepared package ownership = %#v", info.Sys())
	}
}

func (n *syntheticImportedNode) AddEndpoint(endpoint clablinks.Endpoint) error {
	if endpoint.GetLink() == nil || endpoint.GetLink().GetMTU() <= 0 {
		return errors.New("synthetic package endpoint is missing accepted topology Link metadata")
	}
	if endpoint.IsRuntimeDiscovered() && n.Config().NodeType != "pre-realized-endpoint-test" {
		return errors.New("synthetic package endpoint is unexpectedly runtime-discovered")
	}
	requestedName := endpoint.GetIfaceName()
	endpoint.SetIfaceName("mapped-" + requestedName)
	endpoint.SetIfaceAlias(requestedName)

	return n.DefaultNode.AddEndpoint(endpoint)
}

func newSyntheticRegistry(t *testing.T) *clabnodes.NodeRegistry {
	t.Helper()

	registry := clabnodes.NewNodeRegistry()
	err := registry.Register(
		[]string{syntheticKind},
		func() clabnodes.Node { return &syntheticImportedNode{} },
		clabnodes.NewNodeRegistryEntryAttributes(nil, nil, nil).WithPrivilegedByDefault(false),
	)
	if err != nil {
		t.Fatal(err)
	}

	return registry
}

func materializeCertificateRequirements(
	t *testing.T,
	requirements []clabernetesdeviceplan.CertificateRequirement,
) ([]clabernetesdeviceplan.CertificateInput, string) {
	t.Helper()

	if len(requirements) == 0 {
		return nil, ""
	}
	root := t.TempDir()
	ca := clabcert.NewCA()
	caCertificate, err := ca.GenerateCACert(&clabcert.CACSRInput{
		CommonName: "test CA", Country: "US", Organization: "test",
		Expiry: 24 * time.Hour, KeySize: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = ca.SetCACert(caCertificate); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		clabernetesdeviceplan.CertificateCACertKey: caCertificate.Cert,
		clabernetesdeviceplan.CertificateCAKeyKey:  caCertificate.Key,
	} {
		if err = os.WriteFile(filepath.Join(root, name), content, 0o400); err != nil {
			t.Fatal(err)
		}
	}
	inputs := make([]clabernetesdeviceplan.CertificateInput, 0, len(requirements))
	for _, requirement := range requirements {
		hosts := append(slices.Clone(requirement.DNSNames), requirement.IPAddresses...)
		validity := time.Duration(requirement.ValidityNanoseconds)
		if validity == 0 {
			validity = 365 * 24 * time.Hour
		}
		certificate, issueErr := ca.GenerateAndSignNodeCert(&clabcert.NodeCSRInput{
			Hosts: hosts, CommonName: requirement.CommonName,
			Country: requirement.Country, Locality: requirement.Locality,
			Organization:     requirement.Organization,
			OrganizationUnit: requirement.OrganizationalUnit,
			Expiry:           validity, KeySize: requirement.KeySize,
		})
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		certificateKey, privateKeyKey := clabernetesdeviceplan.CertificateMaterialKeys(
			requirement.NodeID,
			requirement.StorageName,
		)
		for name, content := range map[string][]byte{
			certificateKey: certificate.Cert, privateKeyKey: certificate.Key,
		} {
			if err = os.WriteFile(filepath.Join(root, name), content, 0o400); err != nil {
				t.Fatal(err)
			}
		}
		inputs = append(inputs, clabernetesdeviceplan.CertificateInput{
			NodeID: requirement.NodeID, StorageName: requirement.StorageName,
			CertificateDigest:   clabernetesdeviceplan.Digest(certificate.Cert),
			PrivateKeyDigest:    clabernetesdeviceplan.Digest(certificate.Key),
			CACertificateDigest: clabernetesdeviceplan.Digest(caCertificate.Cert),
			CAPrivateKeyDigest:  clabernetesdeviceplan.Digest(caCertificate.Key),
		})
	}

	return inputs, root
}

func TestAdapterAutomaticallyEvaluatesNewRegistryKind(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	evaluation, err := (clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t),
		Revision: "automatic-kind-v1",
	}).Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(evaluation.Nodes), 1; got != want {
		t.Fatalf("evaluated Nodes = %d, want %d", got, want)
	}
	node := evaluation.Nodes[0]
	if node.Config.Kind != syntheticKind || node.Config.ShortName != node.Input.Name {
		t.Fatalf("evaluated config identity = %#v, input = %#v", node.Config, node.Input)
	}
	if node.Config.RestartPolicy != "imported-default" {
		t.Fatalf("restart policy = %q, want imported default", node.Config.RestartPolicy)
	}
	if node.PrivilegedByDefault {
		t.Fatal("registry privilege metadata was not consumed")
	}
	if node.LinkApplyMode != clabernetesdeviceplan.LinkApplyLive {
		t.Fatalf("link apply mode = %q, want Live", node.LinkApplyMode)
	}
	if got, want := node.RuntimeCalls, []string{
		"runtime.Mgmt",
		"runtime.InspectImage",
		"runtime.PullImage",
		"runtime.CreateContainer",
		"runtime.StartContainer",
		"runtime.ListContainers",
		"runtime.ListContainers",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime calls = %#v, want %#v", got, want)
	}
	if got, want := len(node.Images), 1; got != want || node.Images[0].Role != "image" {
		t.Fatalf("imported image roles = %#v, want one primary image", node.Images)
	}
	if got, want := len(node.GeneratedArtifacts), 3; got != want ||
		!hasGeneratedArtifact(node.GeneratedArtifacts, "generated/imported.conf") {
		t.Fatalf("generated artifacts = %#v, want imported runtime files", node.GeneratedArtifacts)
	}
}

func TestImportedHookRuntimePanicRetainsGenericCause(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "predeploy-panic-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	_, err := (clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-panic-v1",
	}).Plan(context.Background(), input)
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesdeviceplan.ErrorUnsupported ||
		planningErr.NodeID != input.Nodes[0].ID ||
		planningErr.Field != "preparation" ||
		planningErr.Behavior != "imported-pre-deploy" {
		t.Fatalf("preparation panic error = %#v, %v", planningErr, err)
	}
	cause := errors.Unwrap(planningErr)
	if cause == nil || !strings.Contains(cause.Error(), "nil pointer dereference") {
		t.Fatalf("preparation panic cause = %v", cause)
	}
}

func TestPackageOwnedPostDeployRunsForNewRegistryKindWithoutC9sRegistration(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "postdeploy-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": "postdeploy-test", "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-post-deploy-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !containsImportedPostDeployAction(plan.Actions, plan.Containers[0].ID) {
		t.Fatalf("package post-deploy plan action = %#v", plan.Actions)
	}
	artifactRoot := t.TempDir()
	nodeRoot := filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	)
	if err = os.MkdirAll(nodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := clabernetesdirectruntime.NewImportedApplicationRuntime(
		input,
		*plan,
		plan.Containers[0].ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = adapter.RunPostDeploy(
		context.Background(),
		input,
		*plan,
		plan.Containers[0].ID,
		t.TempDir(),
		artifactRoot,
		"",
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(nodeRoot, "package-post-deploy-ran"))
	if err != nil || string(marker) != "package-owned" {
		t.Fatalf("package post-deploy marker = %q, %v", marker, err)
	}
}

func TestPackageDeploymentReplayRejectsOperationDrift(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "deployment-replay-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-deployment-drift-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for index := range plan.Actions {
		action := &plan.Actions[index]
		if action.Phase == clabernetesdeviceplan.PhasePostStart &&
			action.Kind == clabernetesdeviceplan.ActionExec && action.Exec != nil {
			action.Exec.Command = []string{"/bin/true"}

			break
		}
	}
	artifactRoot := t.TempDir()
	if err = os.MkdirAll(filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := clabernetesdirectruntime.NewImportedApplicationRuntime(
		input,
		*plan,
		plan.Containers[0].ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.RunPostDeploy(
		context.Background(),
		input,
		*plan,
		plan.Containers[0].ID,
		t.TempDir(),
		artifactRoot,
		"",
		runtime,
	)
	var replayErr *clabernetesdeviceplan.Error
	if !errors.As(err, &replayErr) ||
		replayErr.Code != clabernetesdeviceplan.ErrorInvariant ||
		replayErr.NodeID != input.Nodes[0].ID || replayErr.Field != "deployment.replay" ||
		replayErr.Behavior != "imported-deploy" {
		t.Fatalf("deployment replay drift error = %#v, %v", replayErr, err)
	}
}

func TestPackageDeploymentReplayRejectsComponentInventoryDrift(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "multi-container-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	input.Images = append(input.Images, clabernetesdeviceplan.ImageInput{
		NodeID:          input.Nodes[0].ID,
		ComponentID:     "component-a",
		SourceReference: "example/future-component:1",
		DigestReference: "example/future-component@sha256:" + strings.Repeat("b", 64),
		Platform: clabernetesdeviceplan.Platform{
			OS: "linux", Architecture: "amd64",
		},
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-component-drift-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	primaryID := plan.Nodes[0].ContainerIDs[0]
	if len(plan.Nodes[0].ContainerIDs) != 2 {
		t.Fatalf("component inventory = %#v", plan.Nodes[0].ContainerIDs)
	}
	componentID := plan.Nodes[0].ContainerIDs[1]
	plan.Nodes[0].ReadinessContainerIDs = []string{componentID}
	artifactRoot := t.TempDir()
	if err = os.MkdirAll(filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := clabernetesdirectruntime.NewImportedApplicationRuntime(
		input,
		*plan,
		primaryID,
	)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.RunPostDeploy(
		context.Background(),
		input,
		*plan,
		primaryID,
		t.TempDir(),
		artifactRoot,
		"",
		runtime,
	)
	var replayErr *clabernetesdeviceplan.Error
	if !errors.As(err, &replayErr) ||
		replayErr.Code != clabernetesdeviceplan.ErrorInvariant ||
		replayErr.NodeID != input.Nodes[0].ID || replayErr.Field != "deployment.replay" ||
		replayErr.Behavior != "imported-deploy" {
		t.Fatalf("component replay drift error = %#v, %v", replayErr, err)
	}
}

func TestPackageDeploymentOperationsAreVerifiedWithoutReexecution(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "deployment-replay-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-deployment-replay-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	foundAppend := false
	for _, action := range plan.Actions {
		if action.Kind == clabernetesdeviceplan.ActionFile && action.File != nil &&
			action.File.WriteMode == clabernetesdeviceplan.FileWriteAppend {
			foundAppend = true
		}
	}
	if !foundAppend {
		t.Fatalf("package hosts operation was not mapped as an append: %#v", plan.Actions)
	}
	artifactRoot := t.TempDir()
	nodeRoot := filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	)
	if err = os.MkdirAll(nodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := clabernetesdirectruntime.NewImportedApplicationRuntime(
		input,
		*plan,
		plan.Containers[0].ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = adapter.RunPostDeploy(
		context.Background(),
		input,
		*plan,
		plan.Containers[0].ID,
		t.TempDir(),
		artifactRoot,
		"",
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(nodeRoot, "package-post-deploy-ran"))
	if err != nil || string(marker) != "package-owned" {
		t.Fatalf("package post-deploy marker = %q, %v", marker, err)
	}
}

func TestPackageOwnedLogStreamRunsForNewRegistryKindWithoutC9sRegistration(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "log-stream-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-log-stream-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	nodeRoot := filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	)
	if err = os.MkdirAll(nodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	streamer := &syntheticLogStreamer{}
	broker, err := clabernetesdirectruntime.StartApplicationLogBroker(
		context.Background(),
		t.TempDir()+"/runtime.sock",
		map[string]string{plan.Containers[0].RuntimeID: "kubernetes-device-a"},
		streamer,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	runtime, err := clabernetesdirectruntime.NewImportedApplicationRuntimeWithLogSocket(
		input,
		*plan,
		plan.Containers[0].ID,
		broker.SocketPath(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = adapter.RunPostDeploy(
		context.Background(),
		input,
		*plan,
		plan.Containers[0].ID,
		t.TempDir(),
		artifactRoot,
		"",
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	if got := streamer.Target(); got != "kubernetes-device-a" {
		t.Fatalf("package log target = %q", got)
	}
	raw, err := os.ReadFile(filepath.Join(nodeRoot, "package-log-stream-ran"))
	if err != nil || string(raw) != "package-observed-boot\n" {
		t.Fatalf("package log marker = %q, %v", raw, err)
	}
}

func TestPackageOwnedDeployEndpointsRunsForNewRegistryKindWithoutC9sRegistration(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "postdeploy-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-endpoints-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	nodeRoot := filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	)
	if err = os.MkdirAll(nodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := clabernetesdirectruntime.NewImportedEndpointRuntime(
		input,
		*plan,
		plan.Containers[0].ID,
		"/proc/self/ns/net",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = adapter.RunDeployEndpoints(
		context.Background(),
		input,
		*plan,
		plan.Containers[0].ID,
		t.TempDir(),
		artifactRoot,
		"",
		runtime,
		func(operation func() error) error { return operation() },
	); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(nodeRoot, "package-deploy-endpoints-ran"))
	if err != nil || string(marker) != "package-owned" {
		t.Fatalf("package endpoint marker = %q, %v", marker, err)
	}
	marker, err = os.ReadFile(filepath.Join(nodeRoot, "package-post-deploy-endpoints-ran"))
	if err != nil || string(marker) != "package-owned" {
		t.Fatalf("package post-endpoint marker = %q, %v", marker, err)
	}
}

func TestImportedEndpointLifecycleRetainsTopologyMetadataWithoutRedeployingLink(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "pre-realized-endpoint-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	input.Interfaces = []clabernetesdeviceplan.InterfaceInput{{
		ID: "interface-a", NodeID: input.Nodes[0].ID, Name: "eth1", LinkID: "link-a",
		PeerNodeID: "peer-a", PeerInterface: "eth1", Connectivity: "vxlan",
		PeerTransport: "peer-a-vx", TunnelID: 101, MTU: 9000,
	}}
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-pre-realized-endpoint-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	if err = os.MkdirAll(filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := clabernetesdirectruntime.NewImportedEndpointRuntime(
		input,
		*plan,
		plan.Containers[0].ID,
		"/proc/self/ns/net",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = adapter.RunDeployEndpoints(
		context.Background(),
		input,
		*plan,
		plan.Containers[0].ID,
		t.TempDir(),
		artifactRoot,
		"",
		runtime,
		func(operation func() error) error { return operation() },
	); err != nil {
		t.Fatal(err)
	}
}

func TestImportedEndpointHookFailsByGenericNamespaceCapability(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "endpoint-namespace-capability-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-endpoint-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	if err = os.MkdirAll(filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := clabernetesdirectruntime.NewImportedApplicationRuntime(
		input,
		*plan,
		plan.Containers[0].ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.RunDeployEndpoints(
		context.Background(),
		input,
		*plan,
		plan.Containers[0].ID,
		t.TempDir(),
		artifactRoot,
		"",
		runtime,
		func(operation func() error) error { return operation() },
	)
	var capabilityErr *clabernetesdeviceplan.Error
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Code != clabernetesdeviceplan.ErrorUnsupported ||
		capabilityErr.NodeID != input.Nodes[0].ID ||
		capabilityErr.Field != "runtime.networkNamespace" ||
		capabilityErr.Behavior != "runtime.GetNSPath" {
		t.Fatalf("RunDeployEndpoints() capability error = %#v, %v", capabilityErr, err)
	}
}

func TestImportedEndpointHookFailsByGenericApplicationExecCapability(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "endpoint-application-exec-capability-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": input.Nodes[0].Type, "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-endpoint-exec-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	if err = os.MkdirAll(filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := clabernetesdirectruntime.NewImportedEndpointRuntime(
		input,
		*plan,
		plan.Containers[0].ID,
		"/proc/self/ns/net",
	)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.RunDeployEndpoints(
		context.Background(),
		input,
		*plan,
		plan.Containers[0].ID,
		t.TempDir(),
		artifactRoot,
		"",
		runtime,
		func(operation func() error) error { return operation() },
	)
	var capabilityErr *clabernetesdeviceplan.Error
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Code != clabernetesdeviceplan.ErrorUnsupported ||
		capabilityErr.NodeID != input.Nodes[0].ID ||
		capabilityErr.Field != "runtime.applicationExec" ||
		capabilityErr.Behavior != "runtime.Exec" {
		t.Fatalf("RunDeployEndpoints() capability error = %#v, %v", capabilityErr, err)
	}
}

func TestPackageOwnedReadinessRunsForNewRegistryKindWithoutC9sRegistration(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "readiness-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": "readiness-test", "image": "example/future:1",
	})
	input.Interfaces = []clabernetesdeviceplan.InterfaceInput{{
		ID: "interface-a", NodeID: "node-a", Name: "eth1", LinkID: "link-a",
		Connectivity: "same-pod",
	}}
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-readiness-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !containsImportedReadinessAction(plan.Actions, plan.Containers[0].ID) {
		t.Fatalf("package readiness plan action = %#v", plan.Actions)
	}
	if err = adapter.CheckReadiness(
		context.Background(), input, *plan, plan.Containers[0].ID, t.TempDir(),
	); err != nil {
		t.Fatal(err)
	}

	input.Interfaces = nil
	plan, err = adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err = adapter.CheckReadiness(
		context.Background(), input, *plan, plan.Containers[0].ID, t.TempDir(),
	); err == nil || !strings.Contains(err.Error(), "imported readiness hook is not healthy") {
		t.Fatalf("CheckReadiness() error = %v", err)
	}
}

func TestPackageOwnedSaveRunsForNewRegistryKindWithoutC9sRegistration(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "save-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": "save-test", "image": "example/future:1",
	})
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "package-save-v1",
	}
	plan, err := adapter.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !containsImportedSaveAction(plan.Actions, plan.Containers[0].ID) {
		t.Fatalf("package save plan action = %#v", plan.Actions)
	}
	artifactRoot := t.TempDir()
	nodeRoot := filepath.Join(
		artifactRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Nodes[0].ID),
	)
	if err = os.MkdirAll(nodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := clabernetesdirectruntime.NewImportedApplicationRuntime(
		input,
		*plan,
		plan.Containers[0].ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = adapter.RunSave(
		context.Background(),
		input,
		*plan,
		plan.Containers[0].ID,
		artifactRoot,
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(nodeRoot, "saved.conf"))
	if err != nil || string(saved) != "package-owned-save" {
		t.Fatalf("package save output = %q, %v", saved, err)
	}
}

func containsImportedReadinessAction(
	actions []clabernetesdeviceplan.Action,
	containerID string,
) bool {
	for _, action := range actions {
		if action.Kind == clabernetesdeviceplan.ActionImportedReadiness &&
			action.Phase == clabernetesdeviceplan.PhaseReadiness &&
			action.ImportedReadiness != nil && action.Target.ContainerID == containerID {
			return true
		}
	}

	return false
}

func containsImportedPostDeployAction(
	actions []clabernetesdeviceplan.Action,
	containerID string,
) bool {
	for _, action := range actions {
		if action.Kind == clabernetesdeviceplan.ActionImportedPostDeploy &&
			action.Phase == clabernetesdeviceplan.PhasePostStart &&
			action.ImportedPostDeploy != nil && action.Target.ContainerID == containerID {
			return true
		}
	}

	return false
}

func containsImportedSaveAction(
	actions []clabernetesdeviceplan.Action,
	containerID string,
) bool {
	for _, action := range actions {
		if action.Kind == clabernetesdeviceplan.ActionSave &&
			action.Phase == clabernetesdeviceplan.PhaseSave && action.Save != nil &&
			action.Save.Method == clabernetesdeviceplan.SaveMethodImported &&
			action.Target.ContainerID == containerID {
			return true
		}
	}

	return false
}

func hasGeneratedArtifact(
	artifacts []clabernetesdeviceplan.GeneratedArtifact,
	path string,
) bool {
	for _, artifact := range artifacts {
		if artifact.Path == path {
			return true
		}
	}

	return false
}

func TestAdapterConfinesAndNormalizesImportedInitializerWorkspace(t *testing.T) {
	t.Parallel()

	evaluation, err := (clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t),
		Revision: "workspace-v1",
	}).Evaluate(context.Background(), singleNodeInput(syntheticKind, "example/future:1"))
	if err != nil {
		t.Fatal(err)
	}
	node := evaluation.Nodes[0]
	for _, value := range append(node.Config.Binds, node.Config.Env["IMPORTED_WORKSPACE"]) {
		if strings.Contains(value, "clabernetes-device-plan-") ||
			!strings.Contains(value, "/clabernetes/plan/node-a") {
			t.Fatalf("workspace path was not normalized: %q", value)
		}
	}
}

func TestAdapterRequiresExplicitImageMetadata(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Images = nil
	_, err := (clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t),
		Revision: "test-adapter",
	}).Evaluate(context.Background(), input)
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) ||
		planningErr.Code != clabernetesdeviceplan.ErrorMissingInput ||
		planningErr.Behavior != "runtime.InspectImage" {
		t.Fatalf("Evaluate() error = %#v, want missing image metadata diagnostic", err)
	}
}

func TestAdapterSuppliesDigestVerifiedPayloadPathToImportedHooks(t *testing.T) {
	t.Parallel()

	content := []byte("startup from explicit object\n")
	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "payload-workspace-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]any{
		"kind": syntheticKind, "type": "payload-workspace-test",
		"image": "example/future:1", "startup-config": "/inputs/startup.cfg",
	})
	input.Payloads = []clabernetesdeviceplan.PayloadInput{{
		ID: "startup-input", NodeID: "node-a", Kind: clabernetesdeviceplan.PayloadConfigMap,
		Reference: "lab/device-config:startup.cfg", Digest: clabernetesdeviceplan.Digest(content),
		Destination: "/inputs/startup.cfg", Mode: 0o444,
	}}
	payloadRoot := t.TempDir()
	sourceRoot := filepath.Join(
		payloadRoot,
		clabernetesdeviceplan.ArtifactNodeDirectory(input.Payloads[0].ID),
	)
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "source"), content, 0o444); err != nil {
		t.Fatal(err)
	}

	evaluation, err := (clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "payload-workspace-v1",
		PayloadRoot: payloadRoot,
	}).Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	artifact := "generated/payload-derived.conf"
	if !hasGeneratedArtifact(evaluation.Nodes[0].GeneratedArtifacts, artifact) {
		t.Fatalf(
			"imported payload-derived artifact is absent: %#v",
			evaluation.Nodes[0].GeneratedArtifacts,
		)
	}

	input.Payloads[0].Digest = clabernetesdeviceplan.Digest([]byte("different"))
	_, err = (clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "payload-workspace-v1",
		PayloadRoot: payloadRoot,
	}).Evaluate(context.Background(), input)
	var planningErr *clabernetesdeviceplan.Error
	if !errors.As(err, &planningErr) || planningErr.Code != clabernetesdeviceplan.ErrorInvariant ||
		planningErr.Behavior != "payload-workspace" {
		t.Fatalf("payload digest drift error = %#v", err)
	}
}

func TestImageDiscoverySurfacesPackageImageWhenInitInspectsMissingMetadata(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Images = nil
	discovery, err := (clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t),
		Revision: "two-phase-image-discovery",
	}).DiscoverImages(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Images) != 1 || discovery.Images[0].NodeID != "node-a" ||
		discovery.Images[0].SourceReference != "example/future:1" {
		t.Fatalf("two-phase image discovery = %#v", discovery.Images)
	}
}

func TestImageDiscoveryRetriesMetadataGatedImportedInitializationWithoutKindDispatch(t *testing.T) {
	t.Parallel()

	input := singleNodeInput(syntheticKind, "example/future:1")
	input.Nodes[0].Type = "metadata-gated-test"
	input.Nodes[0].Definition = mustJSON(t, map[string]string{
		"kind": syntheticKind, "type": "metadata-gated-test", "image": "example/future:1",
	})
	input.Images = nil
	adapter := clabernetesdeviceplan.Adapter{
		Registry: newSyntheticRegistry(t), Revision: "metadata-gated-discovery",
	}
	discovery, err := adapter.DiscoverImages(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Images) != 1 || discovery.Images[0].SourceReference != "example/future:1" {
		t.Fatalf("missing metadata requirements = %#v", discovery.Images)
	}
	input.Images = []clabernetesdeviceplan.ImageInput{{
		NodeID: "node-a", Role: discovery.Images[0].Role,
		SourceReference: "example/future:1",
		DigestReference: "example/future@sha256:" + strings.Repeat("a", 64),
		Platform:        clabernetesdeviceplan.Platform{OS: "linux", Architecture: "amd64"},
		Config: clabernetesdeviceplan.ImageConfig{Labels: []clabernetesdeviceplan.KeyValue{{
			Name: "required", Value: "true",
		}}},
	}}
	discovery, err = adapter.DiscoverImages(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Images) != 1 || discovery.Images[0].Role != "image" {
		t.Fatalf("package-owned image roles after metadata = %#v", discovery.Images)
	}
}

func singleNodeInput(kind, image string) clabernetesdeviceplan.Input {
	return clabernetesdeviceplan.Input{
		SchemaVersion: clabernetesdeviceplan.SchemaVersion,
		TopologyName:  "test-topology",
		Compatibility: testCompatibility(),
		Nodes: []clabernetesdeviceplan.NodeInput{{
			ID: "node-a", Name: "router", Kind: kind,
			Definition: []byte(`{"kind":"` + kind + `","image":"` + image + `"}`),
		}},
		Images: []clabernetesdeviceplan.ImageInput{{
			NodeID: "node-a", SourceReference: image,
			DigestReference: image + "@sha256:" + strings.Repeat("a", 64),
			Platform:        clabernetesdeviceplan.Platform{OS: "linux", Architecture: "amd64"},
		}},
	}
}
