//nolint:gocyclo,wsl_v5 // Registry resolution fails closed at each external identity boundary.
package node

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesinternalocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	k8scorev1 "k8s.io/api/core/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// imageMetadataResolveTimeout bounds one registry manifest/config exchange per image.
const imageMetadataResolveTimeout = 60 * time.Second

func compileRegistryMetadataTrust(
	entries []clabernetesapisv1alpha1.RegistryMetadataTrustEntry,
) (*clabernetesinternalocimetadata.RegistryTrustPolicy, error) {
	trust := make([]clabernetesinternalocimetadata.RegistryTrust, 0, len(entries))
	for _, entry := range entries {
		trust = append(trust, clabernetesinternalocimetadata.RegistryTrust{
			Registry:  entry.Registry,
			CABundle:  []byte(entry.CABundle),
			PlainHTTP: entry.PlainHTTP,
		})
	}
	policy, err := clabernetesinternalocimetadata.NewRegistryTrustPolicy(trust)
	if err != nil {
		return nil, planInputError(
			clabernetesinternaldeviceplan.ErrorInvalidInput,
			"config.imagePull.registryMetadataTrust",
			err.Error(),
		)
	}

	return policy, nil
}

// OCIMetadataResolver is satisfied by the bounded metadata cache and by focused test fakes.
type OCIMetadataResolver interface {
	Resolve(
		ctx context.Context,
		request clabernetesinternalocimetadata.Request,
	) (*clabernetesinternalocimetadata.Metadata, error)
}

// ImageMetadataResolution contains non-secret planner metadata plus sensitive values used only to
// prove those bytes never appear in an immutable planner-input ConfigMap.
type ImageMetadataResolution struct {
	Images          []clabernetesinternaldeviceplan.ImageInput
	SensitiveValues [][]byte
	PullSecrets     []k8scorev1.LocalObjectReference
}

// ImageMetadataResolver resolves every imported package-owned image role without downloading a
// layer. It has no kind/component catalog and scopes all credential reads to the Node namespace.
type ImageMetadataResolver struct {
	Client     ctrlruntimeclient.Reader
	Resolver   OCIMetadataResolver
	Platform   clabernetesinternalocimetadata.Platform
	TrustFor   func(reference string) *clabernetesinternalocimetadata.RegistryTrust
	MaxSecrets int
}

// Resolve converts one canonical discovery result into explicit device-plan image inputs.
func (r ImageMetadataResolver) Resolve(
	ctx context.Context,
	namespace string,
	discovery clabernetesinternaldeviceplan.ImageDiscovery,
	pullSecretNames []string,
) (*ImageMetadataResolution, error) {
	if ctx == nil || r.Client == nil || r.Resolver == nil || namespace == "" ||
		r.Platform.OS == "" || r.Platform.Architecture == "" {
		return nil, planInputError(
			clabernetesinternaldeviceplan.ErrorInvalidInput,
			"images",
			"metadata resolver identity, client, namespace, and platform are required",
		)
	}
	normalized, err := clabernetesinternaldeviceplan.NormalizeImageDiscovery(discovery)
	if err != nil {
		return nil, err
	}
	maxSecrets := r.MaxSecrets
	if maxSecrets == 0 {
		maxSecrets = 32
	}
	if maxSecrets < 0 || len(pullSecretNames) > maxSecrets {
		return nil, planInputError(
			clabernetesinternaldeviceplan.ErrorInvalidInput,
			"imagePullSecrets",
			"image pull Secret count exceeds the configured bound",
		)
	}
	result := &ImageMetadataResolution{}
	secrets := make([]k8scorev1.Secret, 0, len(pullSecretNames))
	seenSecrets := map[string]bool{}
	for _, name := range pullSecretNames {
		if strings.TrimSpace(name) == "" || seenSecrets[name] {
			return nil, planInputError(
				clabernetesinternaldeviceplan.ErrorInvalidInput,
				"imagePullSecrets",
				"image pull Secret names must be non-empty and unique",
			)
		}
		secret := &k8scorev1.Secret{}
		if err = r.Client.Get(
			ctx,
			apimachinerytypes.NamespacedName{Namespace: namespace, Name: name},
			secret,
		); err != nil {
			return nil, fmt.Errorf("resolving image pull Secret %s/%s: %w", namespace, name, err)
		}
		seenSecrets[name] = true
		secrets = append(secrets, *secret)
		result.PullSecrets = append(result.PullSecrets, k8scorev1.LocalObjectReference{Name: name})
		for _, value := range secret.Data {
			if len(value) != 0 {
				result.SensitiveValues = append(result.SensitiveValues, slices.Clone(value))
			}
		}
	}

	for _, requirement := range normalized.Images {
		authentication, authErr := clabernetesinternalocimetadata.AuthenticationFromPullSecrets(
			requirement.SourceReference,
			secrets,
		)
		if authErr != nil {
			return nil, authErr
		}
		var trust *clabernetesinternalocimetadata.RegistryTrust
		if r.TrustFor != nil {
			trust = r.TrustFor(requirement.SourceReference)
		}
		// The reconcile context carries no deadline, so bound each registry exchange here;
		// a hung registry must fail this Node instead of stalling the shared worker.
		resolveCtx, cancelResolve := context.WithTimeout(ctx, imageMetadataResolveTimeout)
		metadata, resolveErr := r.Resolver.Resolve(resolveCtx,
			clabernetesinternalocimetadata.Request{
				Reference: requirement.SourceReference, Platform: r.Platform,
				Authentication: authentication, Trust: trust,
			})
		cancelResolve()
		if resolveErr != nil {
			return nil, resolveErr
		}
		image, convertErr := imageInputFromMetadata(requirement, metadata)
		if convertErr != nil {
			return nil, convertErr
		}
		result.Images = append(result.Images, image)
	}

	return result, nil
}

func imageInputFromMetadata(
	requirement clabernetesinternaldeviceplan.ImageRequirement,
	metadata *clabernetesinternalocimetadata.Metadata,
) (clabernetesinternaldeviceplan.ImageInput, error) {
	if metadata == nil || metadata.SourceReference == "" || metadata.DigestReference == "" {
		return clabernetesinternaldeviceplan.ImageInput{}, planInputError(
			clabernetesinternaldeviceplan.ErrorInvariant,
			"images."+requirement.Role,
			"resolved OCI metadata identity differs from the imported image requirement",
		)
	}
	// Config.Hostname is deliberately not inspected: appliance images routinely carry an
	// opaque build-container ID there, and Kubernetes supplies the Pod hostname instead.
	var unsupportedIdentity []string
	if metadata.Config.NetworkDisabled {
		unsupportedIdentity = append(unsupportedIdentity, "networkDisabled")
	}
	if metadata.Config.MacAddress != "" {
		unsupportedIdentity = append(unsupportedIdentity, "macAddress")
	}
	if metadata.Config.Domainname != "" {
		unsupportedIdentity = append(unsupportedIdentity, "domainname")
	}
	if len(unsupportedIdentity) > 0 {
		return clabernetesinternaldeviceplan.ImageInput{}, planInputError(
			clabernetesinternaldeviceplan.ErrorUnsupported,
			"images."+requirement.Role+".config",
			"OCI image requests container-local network identity ("+
				strings.Join(unsupportedIdentity, ", ")+") with no shared-Pod mapping",
		)
	}
	environment := map[string]string{}
	for _, raw := range metadata.Config.Env {
		name, value, _ := strings.Cut(raw, "=")
		if name == "" {
			return clabernetesinternaldeviceplan.ImageInput{}, planInputError(
				clabernetesinternaldeviceplan.ErrorInvalidInput,
				"images."+requirement.Role+".config.env",
				"OCI environment contains an empty name",
			)
		}
		environment[name] = value
	}
	ports := make([]clabernetesinternaldeviceplan.Port, 0, len(metadata.Config.ExposedPorts))
	for _, raw := range metadata.Config.ExposedPorts {
		numberValue, protocol, hasProtocol := strings.Cut(raw, "/")
		if !hasProtocol {
			protocol = "tcp"
		}
		number, parseErr := strconv.Atoi(numberValue)
		protocol = strings.ToUpper(protocol)
		if parseErr != nil || number < 1 || number > 65535 ||
			(protocol != "TCP" && protocol != "UDP") {
			return clabernetesinternaldeviceplan.ImageInput{}, planInputError(
				clabernetesinternaldeviceplan.ErrorUnsupported,
				"images."+requirement.Role+".config.exposedPorts",
				"OCI exposed port has no portable direct-runtime representation",
			)
		}
		ports = append(ports,
			clabernetesinternaldeviceplan.Port{Number: number, Protocol: protocol})
	}
	config := clabernetesinternaldeviceplan.ImageConfig{
		Entrypoint: slices.Clone(metadata.Config.Entrypoint),
		Command:    slices.Clone(metadata.Config.Cmd),
		User:       metadata.Config.User, WorkingDir: metadata.Config.WorkingDir,
		Ports: ports, StopSignal: metadata.Config.StopSignal,
		DeclaredDirs: slices.Clone(metadata.Config.Volumes),
	}
	for name, value := range environment {
		config.Environment = append(config.Environment, clabernetesinternaldeviceplan.KeyValue{
			Name: name, Value: value,
		})
	}
	for _, label := range metadata.Config.Labels {
		config.Labels = append(config.Labels, clabernetesinternaldeviceplan.KeyValue{
			Name: label.Name, Value: label.Value,
		})
	}
	if metadata.Config.Healthcheck != nil {
		config.Healthcheck = &clabernetesinternaldeviceplan.Healthcheck{
			Test:        slices.Clone(metadata.Config.Healthcheck.Test),
			Interval:    int64(metadata.Config.Healthcheck.Interval),
			Timeout:     int64(metadata.Config.Healthcheck.Timeout),
			StartPeriod: int64(metadata.Config.Healthcheck.StartPeriod),
			Retries:     metadata.Config.Healthcheck.Retries,
		}
	}
	slices.SortFunc(config.Environment,
		func(left, right clabernetesinternaldeviceplan.KeyValue) int {
			return strings.Compare(left.Name, right.Name)
		})
	slices.SortFunc(config.Labels, func(left, right clabernetesinternaldeviceplan.KeyValue) int {
		return strings.Compare(left.Name, right.Name)
	})
	slices.SortFunc(ports, func(left, right clabernetesinternaldeviceplan.Port) int {
		if left.Number != right.Number {
			return left.Number - right.Number
		}

		return strings.Compare(left.Protocol, right.Protocol)
	})

	return clabernetesinternaldeviceplan.ImageInput{
		NodeID: requirement.NodeID, Role: requirement.Role,
		SourceReference: metadata.SourceReference, DigestReference: metadata.DigestReference,
		Platform: clabernetesinternaldeviceplan.Platform{
			OS: metadata.Platform.OS, Architecture: metadata.Platform.Architecture,
			Variant: metadata.Platform.Variant, OSVersion: metadata.Platform.OSVersion,
			OSFeatures: slices.Clone(metadata.Platform.OSFeatures),
		},
		Config: config,
	}, nil
}
