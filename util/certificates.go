package util

import (
	"bytes"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
)

const (
	certificateNotAfterYears = 10
)

// CertData is a struct that holds certificate information.
type CertData struct {
	TLS []byte
	CRT []byte
	Key []byte
}

// Write dumps the tls.crt, ca.crt, and tls.key data to disk in the given directory.
func (c *CertData) Write(directory string) error {
	err := os.WriteFile(
		filepath.Join(directory, "tls.crt"),
		c.TLS,
		clabernetesconstants.PermissionsEveryoneReadWrite,
	)
	if err != nil {
		return err
	}

	err = os.WriteFile(
		filepath.Join(directory, "ca.crt"),
		c.CRT,
		clabernetesconstants.PermissionsEveryoneReadWrite,
	)
	if err != nil {
		return err
	}

	return os.WriteFile(
		filepath.Join(directory, "tls.key"),
		c.Key,
		clabernetesconstants.PermissionsEveryoneReadWrite,
	)
}

// CreateCertificateAuthority generates a certificate authority x509.Certificate.
func CreateCertificateAuthority() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: serialNumber(),
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("%s-ca", clabernetesconstants.Clabernetes),
			Organization: []string{clabernetesconstants.Clabernetes},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().AddDate(certificateNotAfterYears, 0, 0),
		IsCA:      true,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
			x509.ExtKeyUsageServerAuth,
		},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
}

// CreateClientCertificate generates a client x509.Certificate with the common name provided.
func CreateClientCertificate(name string) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: serialNumber(),
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("%s-%s", clabernetesconstants.Clabernetes, name),
			Organization: []string{clabernetesconstants.Clabernetes},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().AddDate(certificateNotAfterYears, 0, 0),
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
			x509.ExtKeyUsageServerAuth,
		},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
}

// MustGeneratePrivateKey generates a rsa private key of keySize or panics.
func MustGeneratePrivateKey(keySize int) *rsa.PrivateKey {
	key, err := rsa.GenerateKey(cryptorand.Reader, keySize)
	if err != nil {
		Panic(err.Error())
	}

	return key
}

// GenerateCertificateData generates certificate data (ready to be written to disk) from the given
// cert/ca bytes and key.
func GenerateCertificateData(certBytes, caBytes []byte, key *rsa.PrivateKey) (*CertData, error) {
	cert := &CertData{}

	tlsOut := &bytes.Buffer{}

	err := pem.Encode(tlsOut, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	if err != nil {
		return nil, err
	}

	cert.TLS = tlsOut.Bytes()

	crtOut := &bytes.Buffer{}

	err = pem.Encode(crtOut, &pem.Block{Type: "CERTIFICATE", Bytes: caBytes})
	if err != nil {
		return nil, err
	}

	cert.CRT = crtOut.Bytes()

	keyOut := &bytes.Buffer{}

	err = pem.Encode(
		keyOut,
		&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)},
	)
	if err != nil {
		return nil, err
	}

	cert.Key = keyOut.Bytes()

	return cert, nil
}

func serialNumber() *big.Int {
	return big.NewInt(rand.Int63n(999999999999999)) //nolint:mnd,gosec
}
