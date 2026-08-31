//nolint:funlen,gocognit,gocyclo // single-pass boundary logic reads clearest unsplit.
package deviceplan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"
)

const declaredImageRole = "declared-node-image"

// ImageDiscovery is the deterministic output of the imported kind's image-role hook. The
// controller resolves these references to OCI metadata before requesting a complete plan.
type ImageDiscovery struct {
	SchemaVersion string                   `json:"schemaVersion"`
	Compatibility Compatibility            `json:"compatibility"`
	InputDigest   string                   `json:"inputDigest"`
	Planner       PlannerIdentity          `json:"planner"`
	Images        []ImageRequirement       `json:"images"`
	Certificates  []CertificateRequirement `json:"certificates,omitempty"`
}

// ImageRequirement is one imported image role. Role is opaque package-owned identity.
type ImageRequirement struct {
	NodeID          string `json:"nodeID"`
	Role            string `json:"role"`
	SourceReference string `json:"sourceReference"`
}

// CertificateRequirement is the public certificate request observed from the imported package.
// It is produced by letting the package issue against a disposable CA and parsing the resulting
// public certificate. No c9s kind logic or private certificate material participates.
type CertificateRequirement struct {
	NodeID              string   `json:"nodeID"`
	StorageName         string   `json:"storageName"`
	CommonName          string   `json:"commonName"`
	DNSNames            []string `json:"dnsNames,omitempty"`
	IPAddresses         []string `json:"ipAddresses,omitempty"`
	Country             string   `json:"country,omitempty"`
	Locality            string   `json:"locality,omitempty"`
	Organization        string   `json:"organization,omitempty"`
	OrganizationalUnit  string   `json:"organizationalUnit,omitempty"`
	KeySize             int      `json:"keySize"`
	ValidityNanoseconds int64    `json:"validityNanoseconds,omitempty"`
}

// DiscoverDeclaredImages returns the explicit image references available before imported kind
// initialization. Some package implementations inspect OCI labels during Init in order to derive
// component images or defaults, so these references form the first phase of generic discovery.
// Kind and role identity remain opaque; no registry dispatch participates.
func DiscoverDeclaredImages(input Input, revision string) (*ImageDiscovery, error) {
	if strings.TrimSpace(revision) == "" {
		return nil, planningError(
			ErrorMissingInput,
			"adapter.revision",
			"revision is required",
			nil,
		)
	}

	normalized, err := NormalizeInput(input)
	if err != nil {
		return nil, err
	}

	inputDigest, err := normalized.Digest()
	if err != nil {
		return nil, err
	}

	result := ImageDiscovery{
		SchemaVersion: SchemaVersion,
		Compatibility: normalized.Compatibility,
		InputDigest:   inputDigest,
		Planner:       PlannerIdentity{Name: plannerIdentity, Revision: revision},
	}
	for _, node := range normalized.Nodes {
		definition, decodeErr := decodeNodeDefinition(node)
		if decodeErr != nil {
			return nil, decodeErr
		}

		if strings.TrimSpace(definition.Image) == "" {
			continue
		}

		result.Images = append(result.Images, ImageRequirement{
			NodeID: node.ID, Role: declaredImageRole, SourceReference: definition.Image,
		})
	}

	return NormalizeImageDiscovery(result)
}

// DiscoverImages first records imported initialization and image discovery. Once all explicit OCI
// metadata is present, it runs imported preparation and containerlab's generic issuer against a
// disposable CA so the package's exact public certificate requests can be returned. It does not
// execute deployment, post-deployment, readiness, or any real image/container/network operation.
func (a Adapter) DiscoverImages(ctx context.Context, input Input) (*ImageDiscovery, error) {
	if ctx == nil {
		return nil, planningError(ErrorInvalidInput, "context", "context is nil", nil)
	}

	if strings.TrimSpace(a.Revision) == "" {
		return nil, planningError(
			ErrorMissingInput,
			"adapter.revision",
			"revision is required",
			nil,
		)
	}

	normalized, err := NormalizeInput(input)
	if err != nil {
		return nil, err
	}

	inputDigest, err := normalized.Digest()
	if err != nil {
		return nil, err
	}

	finishEntropy, err := a.beginEntropy(normalized)
	if err != nil {
		return nil, err
	}
	defer finishEntropy()

	registry := a.Registry
	if registry == nil {
		registry = NewContainerlabRegistry()
		if err = validateLiveCompatibility(registry, normalized.Compatibility); err != nil {
			return nil, err
		}
	}

	scratchRoot, err := os.MkdirTemp("", "clabernetes-device-images-")
	if err != nil {
		return nil, planningError(
			ErrorSideEffect,
			fieldWorkspace,
			"cannot create controlled image-discovery workspace",
			err,
		)
	}

	defer func() { _ = os.RemoveAll(scratchRoot) }()

	result := &ImageDiscovery{
		SchemaVersion: SchemaVersion, Compatibility: normalized.Compatibility,
		InputDigest: inputDigest,
		Planner:     PlannerIdentity{Name: plannerIdentity, Revision: a.Revision},
	}
	evaluatedNodes := make([]EvaluatedNode, 0, len(normalized.Nodes))
	metadataComplete := true

	for index, nodeInput := range normalized.Nodes {
		evaluated, evaluateErr := evaluateNode(
			ctx,
			registry,
			normalized,
			nodeInput,
			index,
			scratchRoot,
			a.PayloadRoot,
			false,
		)
		if evaluateErr != nil {
			if metadataRequired, ok := errors.AsType[*imageMetadataRequiredError](evaluateErr); ok {
				metadataComplete = false

				for _, reference := range metadataRequired.references {
					result.Images = append(result.Images, ImageRequirement{
						NodeID:          metadataRequired.nodeID,
						Role:            "runtime-inspect-" + shortDigest(reference),
						SourceReference: reference,
					})
				}

				continue
			}

			return nil, evaluateErr
		}

		evaluatedNodes = append(evaluatedNodes, *evaluated)

		represented := make(map[string]bool, len(evaluated.Images))
		for _, image := range evaluated.Images {
			result.Images = append(result.Images, ImageRequirement{
				NodeID: nodeInput.ID, Role: image.Role, SourceReference: image.Reference,
			})
			represented[image.Reference] = true
		}

		for _, reference := range evaluated.MissingImages {
			if represented[reference] {
				continue
			}

			result.Images = append(result.Images, ImageRequirement{
				NodeID: nodeInput.ID, Role: "runtime-inspect-" + shortDigest(reference),
				SourceReference: reference,
			})
		}

		if validateErr := validateImageInputs(nodeInput.ID,
			evaluated.Images, normalized.Images); validateErr != nil {
			var planningErr *Error
			if errors.As(validateErr, &planningErr) && planningErr.Code == ErrorMissingInput {
				metadataComplete = false
			} else {
				return nil, validateErr
			}
		}
	}

	if metadataComplete && len(evaluatedNodes) == len(normalized.Nodes) {
		infrastructure, storage, certificateErr := newRecordingCertificateInfrastructure()
		if certificateErr != nil {
			return nil, certificateErr
		}

		if certificateErr = discoverCertificateRequests(
			ctx,
			evaluatedNodes,
			normalized.TopologyName,
			infrastructure,
		); certificateErr != nil {
			return nil, certificateErr
		}

		result.Certificates = storage.Requirements()
	}

	return NormalizeImageDiscovery(*result)
}

// CanonicalJSON returns stable, validated image-discovery output.
func (d ImageDiscovery) CanonicalJSON() ([]byte, error) {
	normalized, err := NormalizeImageDiscovery(d)
	if err != nil {
		return nil, err
	}

	return marshalCanonical(normalized, "image discovery")
}

// NormalizeImageDiscovery validates and deterministically orders imported image roles.
func NormalizeImageDiscovery(discovery ImageDiscovery) (*ImageDiscovery, error) {
	normalized, err := cloneJSON(discovery)
	if err != nil {
		return nil, planningError(
			ErrorSerialization,
			"imageDiscovery",
			"cannot clone image discovery",
			err,
		)
	}

	if err = validateHeader(normalized.SchemaVersion, normalized.Compatibility); err != nil {
		return nil, err
	}

	if !validDigest(normalized.InputDigest) || normalized.Planner.Name == "" ||
		normalized.Planner.Revision == "" {
		return nil, planningError(
			ErrorInvalidInput,
			"imageDiscovery",
			"input digest and planner identity are required",
			nil,
		)
	}

	seen := map[string]bool{}

	for index, image := range normalized.Images {
		field := fmt.Sprintf("imageDiscovery.images[%d]", index)
		if image.NodeID == "" || image.Role == "" || image.SourceReference == "" {
			return nil, planningError(
				ErrorMissingInput,
				field,
				"Node, role, and source reference are required",
				nil,
			)
		}

		key := image.NodeID + "\x00" + image.Role
		if seen[key] {
			return nil, planningError(
				ErrorInvariant,
				field,
				"imported image role is duplicated",
				nil,
			)
		}

		seen[key] = true
	}

	slices.SortFunc(normalized.Images, func(left, right ImageRequirement) int {
		if compared := strings.Compare(left.NodeID, right.NodeID); compared != 0 {
			return compared
		}

		return strings.Compare(left.Role, right.Role)
	})

	normalized.Certificates, err = NormalizeCertificateRequirements(normalized.Certificates)
	if err != nil {
		return nil, err
	}

	return &normalized, nil
}

// NormalizeCertificateRequirements validates and orders public requests captured from package
// hooks without requiring a surrounding discovery envelope.
func NormalizeCertificateRequirements(
	requirements []CertificateRequirement,
) ([]CertificateRequirement, error) {
	normalized, err := cloneJSON(requirements)
	if err != nil {
		return nil, planningError(
			ErrorSerialization,
			"certificateRequirements",
			"cannot clone public certificate requests",
			err,
		)
	}

	certificateSeen := map[string]bool{}

	for index := range normalized {
		certificate := &normalized[index]

		field := fmt.Sprintf("certificateRequirements[%d]", index)
		if certificate.NodeID == "" || strings.TrimSpace(certificate.StorageName) == "" ||
			strings.TrimSpace(certificate.CommonName) == "" || certificate.KeySize < 2048 ||
			certificate.KeySize > 8192 || certificate.ValidityNanoseconds < 0 ||
			certificate.ValidityNanoseconds > int64(20*365*24*time.Hour) {
			return nil, planningError(
				ErrorInvalidInput,
				field,
				"public certificate request is incomplete",
				nil,
			)
		}

		key := certificate.NodeID + "\x00" + certificate.StorageName
		if certificateSeen[key] {
			return nil, planningError(
				ErrorInvariant,
				field,
				"public certificate storage identity is duplicated",
				nil,
			)
		}

		certificateSeen[key] = true

		slices.Sort(certificate.DNSNames)
		certificate.DNSNames = slices.Compact(certificate.DNSNames)
		slices.Sort(certificate.IPAddresses)

		certificate.IPAddresses = slices.Compact(certificate.IPAddresses)
		for _, address := range certificate.IPAddresses {
			if net.ParseIP(address) == nil {
				return nil, planningError(
					ErrorInvalidInput,
					field+".ipAddresses",
					"certificate request contains an invalid IP address",
					nil,
				)
			}
		}
	}

	slices.SortFunc(normalized, func(left, right CertificateRequirement) int {
		if compared := strings.Compare(left.NodeID, right.NodeID); compared != 0 {
			return compared
		}

		return strings.Compare(left.StorageName, right.StorageName)
	})

	return normalized, nil
}

// DecodeImageDiscovery rejects unknown fields and trailing JSON.
func DecodeImageDiscovery(raw []byte) (ImageDiscovery, error) {
	decoded, err := decodeStrict[ImageDiscovery](raw, "image discovery")
	if err != nil {
		return ImageDiscovery{}, err
	}

	normalized, err := NormalizeImageDiscovery(decoded)
	if err != nil {
		return ImageDiscovery{}, err
	}

	return *normalized, nil
}
