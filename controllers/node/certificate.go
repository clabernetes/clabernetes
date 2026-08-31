//nolint:err113,funcorder,gocyclo,mnd,nestif,wsl_v5 // Secret reconciliation validates each immutable identity boundary.
package node

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	clabernetesapisv1alpha1 "github.com/clabernetes/clabernetes/apis/v1alpha1"
	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesinternaldeviceplan "github.com/clabernetes/clabernetes/internal/deviceplan"
	clabcert "github.com/srl-labs/containerlab/cert"
	k8scorev1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	directCertificateLabel       = "c9s.run/direct-certificates"
	directCertificateRequest     = "c9s.run/certificate-request-digest"
	directCertificateAuthorityID = "c9s.run/certificate-authority-digest"
	directCertificateAuthority   = "authority"
	directCertificateBundle      = "bundle"
)

// CertificateResolution is the non-secret identity and Secret reference supplied to workers.
type CertificateResolution struct {
	SecretName      string
	Inputs          []clabernetesinternaldeviceplan.CertificateInput
	SensitiveValues [][]byte
}

// CertificateReconciler persists package-requested certificate material independently of plans.
// It has no kind catalog: every subject and SAN originates in imported-hook discovery output.
type CertificateReconciler struct {
	Client ctrlruntimeclient.Client
	Reader ctrlruntimeclient.Reader
}

func (r *CertificateReconciler) reader() ctrlruntimeclient.Reader {
	if r.Reader != nil {
		return r.Reader
	}

	return r.Client
}

// Resolve ensures one shared topology CA and one immutable workload-owned certificate bundle.
func (r *CertificateReconciler) Resolve(
	ctx context.Context,
	owner *clabernetesapisv1alpha1.Node,
	topologyName string,
	requirements []clabernetesinternaldeviceplan.CertificateRequirement,
) (*CertificateResolution, error) {
	if len(requirements) == 0 {
		return &CertificateResolution{}, nil
	}
	if ctx == nil || r == nil || r.Client == nil || owner == nil || owner.GetNamespace() == "" ||
		owner.GetName() == "" || owner.GetUID() == "" || strings.TrimSpace(topologyName) == "" {
		return nil, errors.New("certificate reconciliation identity is incomplete")
	}
	requirements, requestDigest, err := normalizeCertificateRequirements(requirements)
	if err != nil {
		return nil, err
	}
	caSecret, err := r.ensureCertificateAuthority(ctx, owner, topologyName)
	if err != nil {
		return nil, err
	}
	caCertificate := caSecret.Data[clabernetesinternaldeviceplan.CertificateCACertKey]
	caPrivateKey := caSecret.Data[clabernetesinternaldeviceplan.CertificateCAKeyKey]
	caDigest := clabernetesinternaldeviceplan.Digest(caCertificate)
	name := certificateBundleName(owner.GetName(), requestDigest, caDigest)
	existing := &k8scorev1.Secret{}
	err = r.reader().Get(
		ctx,
		ctrlruntimeclient.ObjectKey{Namespace: owner.GetNamespace(), Name: name},
		existing,
	)
	if apimachineryerrors.IsNotFound(err) {
		rendered, renderErr := renderCertificateBundle(
			owner,
			name,
			requestDigest,
			caCertificate,
			caPrivateKey,
			requirements,
		)
		if renderErr != nil {
			return nil, renderErr
		}
		if err = r.Client.Create(ctx, rendered); err != nil {
			if !apimachineryerrors.IsAlreadyExists(err) {
				return nil, fmt.Errorf("creating direct certificate Secret: %w", err)
			}
			existing = &k8scorev1.Secret{}
			if err = r.reader().Get(
				ctx,
				ctrlruntimeclient.ObjectKey{Namespace: owner.GetNamespace(), Name: name},
				existing,
			); err != nil {
				return nil, fmt.Errorf(
					"reading concurrently created direct certificate Secret: %w",
					err,
				)
			}
		} else {
			existing = rendered
		}
	} else if err != nil {
		return nil, fmt.Errorf("reading direct certificate Secret: %w", err)
	}

	return validateCertificateBundle(
		existing,
		owner,
		requestDigest,
		caCertificate,
		caPrivateKey,
		requirements,
	)
}

func (r *CertificateReconciler) ensureCertificateAuthority(
	ctx context.Context,
	owner *clabernetesapisv1alpha1.Node,
	topologyName string,
) (*k8scorev1.Secret, error) {
	name := certificateAuthorityName(topologyName)
	existing := &k8scorev1.Secret{}
	err := r.reader().Get(
		ctx,
		ctrlruntimeclient.ObjectKey{Namespace: owner.GetNamespace(), Name: name},
		existing,
	)
	if apimachineryerrors.IsNotFound(err) {
		ca := clabcert.NewCA()
		certificate, generateErr := ca.GenerateCACert(&clabcert.CACSRInput{
			CommonName: topologyName + " direct device CA",
			Country:    "US", Organization: "clabernetes",
			Expiry: 10 * 365 * 24 * time.Hour, KeySize: 2048,
		})
		if generateErr != nil {
			return nil, fmt.Errorf("generating direct certificate authority: %w", generateErr)
		}
		immutable := true
		existing = &k8scorev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: owner.GetNamespace(),
				Labels: map[string]string{
					// LabelApp keeps the Secret visible to the manager's label-filtered cache.
					clabernetesconstants.LabelApp:            clabernetesconstants.Clabernetes,
					clabernetesconstants.LabelKubernetesName: "clabernetes-direct-certificates",
					directCertificateLabel:                   directCertificateAuthority,
				},
			},
			Immutable: &immutable,
			Type:      k8scorev1.SecretTypeOpaque,
			Data: map[string][]byte{
				clabernetesinternaldeviceplan.CertificateCACertKey: certificate.Cert,
				clabernetesinternaldeviceplan.CertificateCAKeyKey:  certificate.Key,
			},
		}
		if topologyOwner := directTopologyOwnerReference(owner); topologyOwner != nil {
			existing.OwnerReferences = []metav1.OwnerReference{*topologyOwner}
		}
		if err = r.Client.Create(ctx, existing); err != nil {
			if !apimachineryerrors.IsAlreadyExists(err) {
				return nil, fmt.Errorf("creating direct certificate authority Secret: %w", err)
			}
			existing = &k8scorev1.Secret{}
			if err = r.reader().Get(
				ctx,
				ctrlruntimeclient.ObjectKey{Namespace: owner.GetNamespace(), Name: name},
				existing,
			); err != nil {
				return nil, fmt.Errorf(
					"reading concurrently created direct certificate authority Secret: %w",
					err,
				)
			}
		}
	} else if err != nil {
		return nil, fmt.Errorf("reading direct certificate authority Secret: %w", err)
	}
	if existing.Labels[directCertificateLabel] != directCertificateAuthority ||
		existing.Immutable == nil || !*existing.Immutable || len(existing.Data) != 2 {
		return nil, fmt.Errorf("direct certificate authority Secret %s/%s conflicts with policy",
			existing.GetNamespace(), existing.GetName())
	}
	certificate := &clabcert.Certificate{
		Cert: existing.Data[clabernetesinternaldeviceplan.CertificateCACertKey],
		Key:  existing.Data[clabernetesinternaldeviceplan.CertificateCAKeyKey],
	}
	ca := clabcert.NewCA()
	if err = ca.SetCACert(certificate); err != nil {
		return nil, fmt.Errorf("validating direct certificate authority Secret: %w", err)
	}

	return existing, nil
}

func renderCertificateBundle(
	owner *clabernetesapisv1alpha1.Node,
	name,
	requestDigest string,
	caCertificate,
	caPrivateKey []byte,
	requirements []clabernetesinternaldeviceplan.CertificateRequirement,
) (*k8scorev1.Secret, error) {
	ca := clabcert.NewCA()
	authority := &clabcert.Certificate{Cert: caCertificate, Key: caPrivateKey}
	if err := ca.SetCACert(authority); err != nil {
		return nil, fmt.Errorf("initializing direct certificate authority: %w", err)
	}
	data := map[string][]byte{
		clabernetesinternaldeviceplan.CertificateCACertKey: slices.Clone(caCertificate),
		clabernetesinternaldeviceplan.CertificateCAKeyKey:  slices.Clone(caPrivateKey),
	}
	for _, requirement := range requirements {
		hosts := append(slices.Clone(requirement.DNSNames), requirement.IPAddresses...)
		validity := time.Duration(requirement.ValidityNanoseconds)
		if validity == 0 {
			validity = 365 * 24 * time.Hour
		}
		certificate, err := ca.GenerateAndSignNodeCert(&clabcert.NodeCSRInput{
			Hosts: hosts, CommonName: requirement.CommonName,
			Country: requirement.Country, Locality: requirement.Locality,
			Organization:     requirement.Organization,
			OrganizationUnit: requirement.OrganizationalUnit,
			Expiry:           validity, KeySize: requirement.KeySize,
		})
		if err != nil {
			return nil, fmt.Errorf("issuing package-requested node certificate: %w", err)
		}
		certificateKey, privateKeyKey := clabernetesinternaldeviceplan.CertificateMaterialKeys(
			requirement.NodeID,
			requirement.StorageName,
		)
		data[certificateKey] = certificate.Cert
		data[privateKeyKey] = certificate.Key
	}
	immutable := true
	controller := true
	blockOwnerDeletion := true

	return &k8scorev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: owner.GetNamespace(),
			Labels: map[string]string{
				// LabelApp keeps the Secret visible to the manager's label-filtered cache so
				// the owner watch fires for it.
				clabernetesconstants.LabelApp:            clabernetesconstants.Clabernetes,
				clabernetesconstants.LabelKubernetesName: "clabernetes-direct-certificates",
				directCertificateLabel:                   directCertificateBundle,
			},
			Annotations: map[string]string{
				directCertificateRequest:     requestDigest,
				directCertificateAuthorityID: clabernetesinternaldeviceplan.Digest(caCertificate),
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clabernetesapisv1alpha1.SchemeGroupVersion.String(), Kind: nodeCRKind,
				Name: owner.GetName(), UID: owner.GetUID(),
				Controller: &controller, BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Immutable: &immutable, Type: k8scorev1.SecretTypeOpaque, Data: data,
	}, nil
}

func validateCertificateBundle(
	secret *k8scorev1.Secret,
	owner *clabernetesapisv1alpha1.Node,
	requestDigest string,
	caCertificate,
	caPrivateKey []byte,
	requirements []clabernetesinternaldeviceplan.CertificateRequirement,
) (*CertificateResolution, error) {
	if secret == nil || secret.Labels[directCertificateLabel] != directCertificateBundle ||
		secret.Annotations[directCertificateRequest] != requestDigest ||
		secret.Annotations[directCertificateAuthorityID] !=
			clabernetesinternaldeviceplan.Digest(caCertificate) ||
		secret.Immutable == nil || !*secret.Immutable || len(secret.OwnerReferences) != 1 ||
		secret.OwnerReferences[0].UID != owner.GetUID() ||
		!slices.Equal(secret.Data[clabernetesinternaldeviceplan.CertificateCACertKey],
			caCertificate) ||
		!slices.Equal(secret.Data[clabernetesinternaldeviceplan.CertificateCAKeyKey],
			caPrivateKey) ||
		len(secret.Data) != 2+len(requirements)*2 {
		return nil, errors.New("direct certificate Secret conflicts with accepted identity")
	}
	ca, err := parseCertificate(caCertificate)
	if err != nil {
		return nil, fmt.Errorf("parsing direct certificate authority: %w", err)
	}
	result := &CertificateResolution{SecretName: secret.GetName()}
	result.SensitiveValues = append(
		result.SensitiveValues,
		slices.Clone(caCertificate),
		slices.Clone(caPrivateKey),
	)
	for _, requirement := range requirements {
		certificateKey, privateKeyKey := clabernetesinternaldeviceplan.CertificateMaterialKeys(
			requirement.NodeID,
			requirement.StorageName,
		)
		certificate := secret.Data[certificateKey]
		privateKey := secret.Data[privateKeyKey]
		if _, err = tls.X509KeyPair(certificate, privateKey); err != nil {
			return nil, fmt.Errorf("validating package-requested certificate key pair: %w", err)
		}
		parsed, parseErr := parseCertificate(certificate)
		if parseErr != nil || parsed.CheckSignatureFrom(ca) != nil ||
			!certificateMatchesRequirement(parsed, requirement) {
			return nil, errors.New("issued certificate differs from package request")
		}
		result.Inputs = append(result.Inputs, clabernetesinternaldeviceplan.CertificateInput{
			NodeID: requirement.NodeID, StorageName: requirement.StorageName,
			CertificateDigest:   clabernetesinternaldeviceplan.Digest(certificate),
			PrivateKeyDigest:    clabernetesinternaldeviceplan.Digest(privateKey),
			CACertificateDigest: clabernetesinternaldeviceplan.Digest(caCertificate),
			CAPrivateKeyDigest:  clabernetesinternaldeviceplan.Digest(caPrivateKey),
		})
		result.SensitiveValues = append(
			result.SensitiveValues,
			slices.Clone(certificate),
			slices.Clone(privateKey),
		)
	}

	return result, nil
}

func normalizeCertificateRequirements(
	requirements []clabernetesinternaldeviceplan.CertificateRequirement,
) ([]clabernetesinternaldeviceplan.CertificateRequirement, string, error) {
	normalized, err := clabernetesinternaldeviceplan.NormalizeCertificateRequirements(requirements)
	if err != nil {
		return nil, "", err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, "", fmt.Errorf("serializing package certificate requirements: %w", err)
	}

	return normalized, clabernetesinternaldeviceplan.Digest(raw), nil
}

func certificateMatchesRequirement(
	certificate *x509.Certificate,
	requirement clabernetesinternaldeviceplan.CertificateRequirement,
) bool {
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok || publicKey.N.BitLen() != requirement.KeySize ||
		certificate.Subject.CommonName != requirement.CommonName ||
		firstString(certificate.Subject.Country) != requirement.Country ||
		firstString(certificate.Subject.Locality) != requirement.Locality ||
		firstString(certificate.Subject.Organization) != requirement.Organization ||
		firstString(certificate.Subject.OrganizationalUnit) != requirement.OrganizationalUnit {
		return false
	}
	dnsNames := slices.Clone(certificate.DNSNames)
	slices.Sort(dnsNames)
	ipAddresses := make([]string, 0, len(certificate.IPAddresses))
	for _, address := range certificate.IPAddresses {
		ipAddresses = append(ipAddresses, address.String())
	}
	slices.Sort(ipAddresses)

	return slices.Equal(dnsNames, requirement.DNSNames) &&
		slices.Equal(ipAddresses, requirement.IPAddresses)
}

func parseCertificate(raw []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("certificate is not PEM encoded")
	}

	return x509.ParseCertificate(block.Bytes)
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

func certificateAuthorityName(topologyName string) string {
	suffix := strings.TrimPrefix(clabernetesinternaldeviceplan.Digest([]byte(topologyName)),
		"sha256:")[:16]

	return "direct-device-ca-" + suffix
}

func certificateBundleName(ownerName, requestDigest, caDigest string) string {
	suffix := strings.TrimPrefix(
		clabernetesinternaldeviceplan.Digest([]byte(requestDigest+"\x00"+caDigest)),
		"sha256:",
	)[:16]
	const separator = "-certificates-"
	maximumOwnerLength := kubernetesNameLimit - len(separator) - len(suffix)
	if len(ownerName) > maximumOwnerLength {
		ownerName = strings.TrimRight(ownerName[:maximumOwnerLength], "-")
	}

	return ownerName + separator + suffix
}

func directTopologyOwnerReference(node *clabernetesapisv1alpha1.Node) *metav1.OwnerReference {
	topologyName := node.GetLabels()[clabernetesconstants.LabelTopologyOwner]
	for _, reference := range node.GetOwnerReferences() {
		if reference.Kind == topologyOwnerKind && reference.Name == topologyName {
			duplicate := reference

			return &duplicate
		}
	}

	return nil
}
