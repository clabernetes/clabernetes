//nolint:err113,funcorder,mnd // structured one-off diagnostics and protocol literals are the design here.
package deviceplan

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	clabcert "github.com/srl-labs/containerlab/cert"
	clabnodes "github.com/srl-labs/containerlab/nodes"
	clabtypes "github.com/srl-labs/containerlab/types"
)

const (
	// CertificateCACertKey and CertificateCAKeyKey are the stable Secret keys and mounted
	// CertificateCACertKey is the projected Secret key holding the CA certificate.
	CertificateCACertKey = "ca.crt"
	// CertificateCAKeyKey is the projected Secret key holding the CA private key.
	CertificateCAKeyKey = "ca.key"
	maxCertificateBytes = 1 << 20
)

// CertificateMaterialKeys returns deterministic Secret keys for one package storage identity.
// StorageName is hashed because it is opaque package data and Kubernetes Secret keys are narrow.
func CertificateMaterialKeys(nodeID, storageName string) (certificateKey, privateKeyKey string) {
	suffix := strings.TrimPrefix(Digest([]byte(nodeID+"\x00"+storageName)), "sha256:")[:24]

	return "node-" + suffix + ".crt", "node-" + suffix + ".key"
}

func mountedCertificateInfrastructure(
	inputs []CertificateInput,
	root string,
) (*clabcert.Cert, error) {
	if len(inputs) == 0 {
		return nil, nil //nolint:nilnil // no certificate inputs means no infrastructure to mount.
	}

	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return nil, planningError(
			ErrorMissingInput,
			fieldCertificates,
			"package-requested certificates require a scoped Secret projection",
			nil,
		)
	}

	caCertificate, err := readCertificateFile(filepath.Join(root, CertificateCACertKey))
	if err != nil {
		return nil, err
	}

	caPrivateKey, err := readCertificateFile(filepath.Join(root, CertificateCAKeyKey))
	if err != nil {
		return nil, err
	}

	storage := &mountedCertificateStorage{
		ca:    &clabcert.Certificate{Cert: caCertificate, Key: caPrivateKey},
		nodes: make(map[string]*clabcert.Certificate, len(inputs)),
	}
	for _, input := range inputs {
		if Digest(caCertificate) != input.CACertificateDigest ||
			Digest(caPrivateKey) != input.CAPrivateKeyDigest {
			return nil, &Error{
				Code: ErrorInvariant, NodeID: input.NodeID, Field: fieldCertificates,
				Behavior: behaviorCertificateMaterial,
				Message:  "mounted certificate authority differs from accepted metadata",
			}
		}

		certificateKey, privateKeyKey := CertificateMaterialKeys(input.NodeID, input.StorageName)

		certificate, readErr := readCertificateFile(filepath.Join(root, certificateKey))
		if readErr != nil {
			return nil, withNodeID(readErr, input.NodeID)
		}

		privateKey, readErr := readCertificateFile(filepath.Join(root, privateKeyKey))
		if readErr != nil {
			return nil, withNodeID(readErr, input.NodeID)
		}

		if Digest(certificate) != input.CertificateDigest ||
			Digest(privateKey) != input.PrivateKeyDigest {
			return nil, &Error{
				Code: ErrorInvariant, NodeID: input.NodeID, Field: fieldCertificates,
				Behavior: behaviorCertificateMaterial,
				Message:  "mounted node certificate differs from accepted metadata",
			}
		}

		if _, exists := storage.nodes[input.StorageName]; exists {
			return nil, &Error{
				Code: ErrorInvariant, NodeID: input.NodeID, Field: "certificates.storageName",
				Behavior: behaviorCertificateMaterial,
				Message:  "package certificate storage name is ambiguous across the workload group",
			}
		}

		storage.nodes[input.StorageName] = &clabcert.Certificate{
			Cert: slices.Clone(certificate), Key: slices.Clone(privateKey),
		}
	}

	ca := clabcert.NewCA()
	if err = ca.SetCACert(storage.ca); err != nil {
		return nil, planningError(
			ErrorInvalidInput,
			"certificates.ca",
			"mounted certificate authority is invalid",
			err,
		)
	}

	return &clabcert.Cert{CA: ca, CertStorage: storage}, nil
}

func readCertificateFile(path string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // Path is below a scoped read-only Secret projection.
	if err != nil {
		return nil, planningError(
			ErrorMissingInput,
			fieldCertificates,
			"cannot read projected certificate material",
			err,
		)
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 ||
		info.Size() > maxCertificateBytes {
		return nil, planningError(
			ErrorInvalidInput,
			fieldCertificates,
			"projected certificate material is not a bounded regular file",
			err,
		)
	}

	content, err := io.ReadAll(io.LimitReader(file, maxCertificateBytes+1))
	if err != nil || len(content) < 1 || len(content) > maxCertificateBytes {
		return nil, planningError(
			ErrorSideEffect,
			fieldCertificates,
			"cannot read complete projected certificate material",
			err,
		)
	}

	return content, nil
}

type mountedCertificateStorage struct {
	ca    *clabcert.Certificate
	nodes map[string]*clabcert.Certificate
}

func (s *mountedCertificateStorage) LoadCaCert() (*clabcert.Certificate, error) {
	return cloneCertificate(s.ca), nil
}

func (s *mountedCertificateStorage) LoadNodeCert(name string) (*clabcert.Certificate, error) {
	certificate := s.nodes[name]
	if certificate == nil {
		return nil, errors.New("package requested undeclared node certificate material")
	}

	return cloneCertificate(certificate), nil
}

func (*mountedCertificateStorage) StoreCaCert(*clabcert.Certificate) error {
	return errors.New("package attempted to replace mounted certificate authority material")
}

func (*mountedCertificateStorage) StoreNodeCert(string, *clabcert.Certificate) error {
	return errors.New("package attempted to create undeclared node certificate material")
}

type recordingCertificateStorage struct {
	ca           *clabcert.Certificate
	activeNodeID string
	nodes        map[string]*clabcert.Certificate
	requirements map[string]CertificateRequirement
}

func newRecordingCertificateInfrastructure() (*clabcert.Cert, *recordingCertificateStorage, error) {
	ca := clabcert.NewCA()

	caCertificate, err := ca.GenerateCACert(&clabcert.CACSRInput{
		CommonName: "disposable package certificate discovery CA",
		Country:    "US", Organization: plannerIdentity, Expiry: 24 * time.Hour, KeySize: 2048,
	})
	if err != nil {
		return nil, nil, planningError(
			ErrorSideEffect,
			"certificates.discovery",
			"cannot create disposable certificate discovery authority",
			err,
		)
	}

	if err = ca.SetCACert(caCertificate); err != nil {
		return nil, nil, planningError(
			ErrorInvariant,
			"certificates.discovery",
			"cannot initialize disposable certificate discovery authority",
			err,
		)
	}

	storage := &recordingCertificateStorage{
		ca: caCertificate, nodes: map[string]*clabcert.Certificate{},
		requirements: map[string]CertificateRequirement{},
	}

	return &clabcert.Cert{CA: ca, CertStorage: storage}, storage, nil
}

func (s *recordingCertificateStorage) activate(nodeID string) {
	s.activeNodeID = nodeID
}

func activateCertificateStorage(infrastructure *clabcert.Cert, nodeID string) {
	if infrastructure == nil {
		return
	}

	if storage, ok := infrastructure.CertStorage.(*recordingCertificateStorage); ok {
		storage.activate(nodeID)
	}
}

func (s *recordingCertificateStorage) LoadCaCert() (*clabcert.Certificate, error) {
	return cloneCertificate(s.ca), nil
}

func (s *recordingCertificateStorage) LoadNodeCert(name string) (*clabcert.Certificate, error) {
	certificate := s.nodes[name]
	if certificate == nil {
		return nil, errors.New("certificate has not been issued in this discovery attempt")
	}

	return cloneCertificate(certificate), nil
}

func (s *recordingCertificateStorage) StoreCaCert(certificate *clabcert.Certificate) error {
	if certificate == nil {
		return errors.New("cannot store an empty certificate authority")
	}

	s.ca = cloneCertificate(certificate)

	return nil
}

func (s *recordingCertificateStorage) StoreNodeCert(
	name string,
	certificate *clabcert.Certificate,
) error {
	if s.activeNodeID == "" || strings.TrimSpace(name) == "" || certificate == nil {
		return errors.New("certificate request has no active Node or storage identity")
	}

	parsed, err := parsePublicCertificate(certificate.Cert)
	if err != nil {
		return err
	}

	publicKey, ok := parsed.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("package-generated node certificate does not use an RSA public key")
	}

	requirement := CertificateRequirement{
		NodeID: s.activeNodeID, StorageName: name,
		CommonName: parsed.Subject.CommonName,
		DNSNames:   slices.Clone(parsed.DNSNames), KeySize: publicKey.N.BitLen(),
		ValidityNanoseconds: parsed.NotAfter.Sub(parsed.NotBefore).Nanoseconds(),
		Country:             firstSubjectValue(parsed.Subject.Country),
		Locality:            firstSubjectValue(parsed.Subject.Locality),
		Organization:        firstSubjectValue(parsed.Subject.Organization),
		OrganizationalUnit:  firstSubjectValue(parsed.Subject.OrganizationalUnit),
	}
	for _, address := range parsed.IPAddresses {
		requirement.IPAddresses = append(requirement.IPAddresses, address.String())
	}

	identity := requirement.NodeID + "\x00" + requirement.StorageName
	if existing, exists := s.requirements[identity]; exists &&
		!reflect.DeepEqual(existing, requirement) {
		return errors.New("package changed a certificate request during one lifecycle evaluation")
	}

	s.requirements[identity] = requirement
	s.nodes[name] = cloneCertificate(certificate)

	return nil
}

func (s *recordingCertificateStorage) Requirements() []CertificateRequirement {
	result := make([]CertificateRequirement, 0, len(s.requirements))
	for _, requirement := range s.requirements {
		result = append(result, requirement)
	}

	return result
}

func cloneCertificate(certificate *clabcert.Certificate) *clabcert.Certificate {
	if certificate == nil {
		return nil
	}

	return &clabcert.Certificate{
		Cert: slices.Clone(certificate.Cert),
		Key:  slices.Clone(certificate.Key),
		Csr:  slices.Clone(certificate.Csr),
	}
}

func parsePublicCertificate(raw []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("package-generated certificate is not PEM encoded")
	}

	return x509.ParseCertificate(block.Bytes)
}

func firstSubjectValue(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

func nodeHasCertificateInput(inputs []CertificateInput, nodeID string) bool {
	return slices.ContainsFunc(inputs, func(input CertificateInput) bool {
		return input.NodeID == nodeID
	})
}

// discoverCertificateRequests lets imported preparation expose any certificate mutations, then
// asks containerlab's own generic issuer for a public request when the resulting package config
// enables issuance. A future hook can therefore request a certificate without c9s recognizing its
// kind, while nodes that do not request one receive no c9s-invented certificate behavior.
// Preparation errors are deferred to full planning; they must not prevent this metadata-only phase
// from obtaining a package-requested generic request.
func discoverCertificateRequests(
	ctx context.Context,
	nodes []EvaluatedNode,
	topologyName string,
	infrastructure *clabcert.Cert,
) error {
	for index := range nodes {
		node := &nodes[index]
		activateCertificateStorage(infrastructure, node.Input.ID)
		_ = invokeImported(
			node.Input.ID,
			"certificates.discovery",
			"imported-certificate-preparation",
			"containerlab certificate preparation panicked",
			func() error {
				return node.implementation.PreDeploy(ctx, &clabnodes.PreDeployParams{
					Cert: infrastructure, TopologyName: topologyName,
				})
			},
		)

		config := node.implementation.Config()
		if config == nil {
			return &Error{
				Code: ErrorInvariant, NodeID: node.Input.ID, Field: fieldCertificates,
				Behavior: behaviorImportedCertificateRequest,
				Message:  "package node returned no configuration during certificate discovery",
			}
		}

		if config.Certificate == nil || config.Certificate.Issue == nil ||
			!*config.Certificate.Issue {
			continue
		}

		overwrites, ok := node.implementation.(clabnodes.NodeOverwrites)
		if !ok {
			return &Error{
				Code: ErrorUnsupported, NodeID: node.Input.ID, Field: fieldCertificates,
				Behavior: behaviorImportedCertificateRequest,
				Message:  "certificate-issuing package node lacks the generic overwrite interface",
			}
		}

		configCopy := *config

		certificateCopy := &clabtypes.CertificateConfig{}
		if config.Certificate != nil {
			*certificateCopy = *config.Certificate
			certificateCopy.SANs = slices.Clone(config.Certificate.SANs)
		}

		issue := true
		certificateCopy.Issue = &issue
		configCopy.Certificate = certificateCopy
		genericNode := clabnodes.NewDefaultNode(overwrites)
		genericNode.Cfg = &configCopy

		err := invokeImported(
			node.Input.ID,
			fieldCertificates,
			behaviorImportedCertificateRequest,
			"containerlab generic certificate request panicked",
			func() error {
				_, issueErr := genericNode.LoadOrGenerateCertificate(infrastructure, topologyName)

				return issueErr
			},
		)
		if err != nil {
			return &Error{
				Code: ErrorUnsupported, NodeID: node.Input.ID, Field: fieldCertificates,
				Behavior: behaviorImportedCertificateRequest,
				Message:  "containerlab generic certificate request could not be discovered",
				cause:    err,
			}
		}
	}

	return nil
}
