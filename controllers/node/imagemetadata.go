//nolint:nlreturn,wsl_v5 // Registry resolution fails closed at each external identity boundary.
package node

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesdeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabernetesocimetadata "github.com/clabernetes/clabernetes/internal/ocimetadata"
	k8scorev1 "k8s.io/api/core/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// imageMetadataResolveTimeout bounds one registry manifest/config exchange per image.
const imageMetadataResolveTimeout = 60 * time.Second

func compileRegistryMetadataTrust(
	entries []clabernetesapisv1alpha1.RegistryMetadataTrustEntry,
) (*clabernetesocimetadata.RegistryTrustPolicy, error) {
	trust := make([]clabernetesocimetadata.RegistryTrust, 0, len(entries))
	for _, entry := range entries {
		trust = append(trust, clabernetesocimetadata.RegistryTrust{
			Registry:  entry.Registry,
			CABundle:  []byte(entry.CABundle),
			PlainHTTP: entry.PlainHTTP,
		})
	}
	policy, err := clabernetesocimetadata.NewRegistryTrustPolicy(trust)
	if err != nil {
		return nil, planInputError(
			clabernetesdeviceplan.ErrorInvalidInput,
			"config.imagePull.registryMetadataTrust",
			err.Error(),
		)
	}

	return policy, nil
}

// OCIMetadataResolver is satisfied by the bounded metadata cache and by focused test fakes.
type OCIMetadataResolver interface {
	Resolve(
		context.Context,
		clabernetesocimetadata.Request,
	) (*clabernetesocimetadata.Metadata, error)
}

// ImageMetadataResolution contains non-secret planner metadata plus sensitive values used only to
// prove those bytes never appear in an immutable planner-input ConfigMap.
type ImageMetadataResolution struct {
	Images          []clabernetesdeviceplan.ImageInput
	SensitiveValues [][]byte
	PullSecrets     []k8scorev1.LocalObjectReference
}

// ImageMetadataResolver resolves every imported package-owned image role without downloading a
// layer. It has no kind/component catalog and scopes all credential reads to the Node namespace.
type ImageMetadataResolver struct {
	Client     ctrlruntimeclient.Reader
	Resolver   OCIMetadataResolver
	Platform   clabernetesocimetadata.Platform
	TrustFor   func(reference string) *clabernetesocimetadata.RegistryTrust
	MaxSecrets int
}

// Resolve converts one canonical discovery result into explicit device-plan image inputs.
func (r ImageMetadataResolver) Resolve(
	ctx context.Context,
	namespace string,
	discovery clabernetesdeviceplan.ImageDiscovery,
	pullSecretNames []string,
) (*ImageMetadataResolution, error) {
	if ctx == nil || r.Client == nil || r.Resolver == nil || namespace == "" ||
		r.Platform.OS == "" || r.Platform.Architecture == "" {
		return nil, planInputError(
			clabernetesdeviceplan.ErrorInvalidInput,
			"images",
			"metadata resolver identity, client, namespace, and platform are required",
		)
	}
	normalized, err := clabernetesdeviceplan.NormalizeImageDiscovery(discovery)
	if err != nil {
		return nil, err
	}
	maxSecrets := r.MaxSecrets
	if maxSecrets == 0 {
		maxSecrets = 32
	}
	if maxSecrets < 0 || len(pullSecretNames) > maxSecrets {
		return nil, planInputError(
			clabernetesdeviceplan.ErrorInvalidInput,
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
				clabernetesdeviceplan.ErrorInvalidInput,
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
		authentication, authErr := clabernetesocimetadata.AuthenticationFromPullSecrets(
			requirement.SourceReference,
			secrets,
		)
		if authErr != nil {
			return nil, authErr
		}
		var trust *clabernetesocimetadata.RegistryTrust
		if r.TrustFor != nil {
			trust = r.TrustFor(requirement.SourceReference)
		}
		// The reconcile context carries no deadline, so bound each registry exchange here;
		// a hung registry must fail this Node instead of stalling the shared worker.
		resolveCtx, cancelResolve := context.WithTimeout(ctx, imageMetadataResolveTimeout)
		metadata, resolveErr := r.Resolver.Resolve(resolveCtx, clabernetesocimetadata.Request{
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
	requirement clabernetesdeviceplan.ImageRequirement,
	metadata *clabernetesocimetadata.Metadata,
) (clabernetesdeviceplan.ImageInput, error) {
	if metadata == nil || metadata.SourceReference == "" || metadata.DigestReference == "" {
		return clabernetesdeviceplan.ImageInput{}, planInputError(
			clabernetesdeviceplan.ErrorInvariant,
			"images."+requirement.Role,
			"resolved OCI metadata identity differs from the imported image requirement",
		)
	}
	if metadata.Config.NetworkDisabled || metadata.Config.MacAddress != "" ||
		metadata.Config.Hostname != "" || metadata.Config.Domainname != "" {
		return clabernetesdeviceplan.ImageInput{}, planInputError(
			clabernetesdeviceplan.ErrorUnsupported,
			"images."+requirement.Role+".config",
			"OCI image requests container-local network identity with no shared-Pod mapping",
		)
	}
	environment := map[string]string{}
	for _, raw := range metadata.Config.Env {
		name, value, _ := strings.Cut(raw, "=")
		if name == "" {
			return clabernetesdeviceplan.ImageInput{}, planInputError(
				clabernetesdeviceplan.ErrorInvalidInput,
				"images."+requirement.Role+".config.env",
				"OCI environment contains an empty name",
			)
		}
		environment[name] = value
	}
	ports := make([]clabernetesdeviceplan.Port, 0, len(metadata.Config.ExposedPorts))
	for _, raw := range metadata.Config.ExposedPorts {
		numberValue, protocol, hasProtocol := strings.Cut(raw, "/")
		if !hasProtocol {
			protocol = "tcp"
		}
		number, parseErr := strconv.Atoi(numberValue)
		protocol = strings.ToUpper(protocol)
		if parseErr != nil || number < 1 || number > 65535 ||
			(protocol != "TCP" && protocol != "UDP") {
			return clabernetesdeviceplan.ImageInput{}, planInputError(
				clabernetesdeviceplan.ErrorUnsupported,
				"images."+requirement.Role+".config.exposedPorts",
				"OCI exposed port has no portable direct-runtime representation",
			)
		}
		ports = append(ports, clabernetesdeviceplan.Port{Number: number, Protocol: protocol})
	}
	config := clabernetesdeviceplan.ImageConfig{
		Entrypoint: slices.Clone(metadata.Config.Entrypoint),
		Command:    slices.Clone(metadata.Config.Cmd),
		User:       metadata.Config.User, WorkingDir: metadata.Config.WorkingDir,
		Ports: ports, StopSignal: metadata.Config.StopSignal,
		DeclaredDirs: slices.Clone(metadata.Config.Volumes),
	}
	for name, value := range environment {
		config.Environment = append(config.Environment, clabernetesdeviceplan.KeyValue{
			Name: name, Value: value,
		})
	}
	for _, label := range metadata.Config.Labels {
		config.Labels = append(config.Labels, clabernetesdeviceplan.KeyValue{
			Name: label.Name, Value: label.Value,
		})
	}
	if metadata.Config.Healthcheck != nil {
		config.Healthcheck = &clabernetesdeviceplan.Healthcheck{
			Test:        slices.Clone(metadata.Config.Healthcheck.Test),
			Interval:    int64(metadata.Config.Healthcheck.Interval),
			Timeout:     int64(metadata.Config.Healthcheck.Timeout),
			StartPeriod: int64(metadata.Config.Healthcheck.StartPeriod),
			Retries:     metadata.Config.Healthcheck.Retries,
		}
	}
	slices.SortFunc(config.Environment, func(left, right clabernetesdeviceplan.KeyValue) int {
		return strings.Compare(left.Name, right.Name)
	})
	slices.SortFunc(config.Labels, func(left, right clabernetesdeviceplan.KeyValue) int {
		return strings.Compare(left.Name, right.Name)
	})
	slices.SortFunc(ports, func(left, right clabernetesdeviceplan.Port) int {
		if left.Number != right.Number {
			return left.Number - right.Number
		}

		return strings.Compare(left.Protocol, right.Protocol)
	})

	return clabernetesdeviceplan.ImageInput{
		NodeID: requirement.NodeID, Role: requirement.Role,
		SourceReference: metadata.SourceReference, DigestReference: metadata.DigestReference,
		Platform: clabernetesdeviceplan.Platform{
			OS: metadata.Platform.OS, Architecture: metadata.Platform.Architecture,
			Variant: metadata.Platform.Variant, OSVersion: metadata.Platform.OSVersion,
			OSFeatures: slices.Clone(metadata.Platform.OSFeatures),
		},
		Config: config,
	}, nil
}
