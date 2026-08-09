package manager

import (
	cryptorand "crypto/rand"
	"crypto/x509"
	"fmt"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	clabernetesmanagertypes "github.com/clabernetes/clabernetes/manager/types"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
	k8scorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func initializeCertificates(c clabernetesmanagertypes.Clabernetes) error { //nolint:funlen
	logger := c.GetBaseLogger()

	secret, err := getCertificatesSecret(c)
	if err != nil {
		return fmt.Errorf("getting certificates secret: %w", err)
	}

	certsExist := true

	if secret.Data != nil {
		for _, certType := range []string{"ca", "client", "webhook"} {
			for _, fileName := range []string{"ca.crt", "tls.crt", "tls.key"} {
				_, ok := secret.Data[fmt.Sprintf("%s-%s", certType, fileName)]
				if !ok {
					certsExist = false

					break
				}
			}

			if !certsExist {
				break
			}
		}
	} else {
		secret.Data = map[string][]byte{}

		certsExist = false
	}

	if certsExist {
		logger.Info("all certificates secret data present, nothing to do")

		return nil
	}

	logger.Debug(
		"certificates secret does not contain all required certificate data, generating...",
	)

	// ca
	ca := clabernetesutil.CreateCertificateAuthority()

	caKey := clabernetesutil.MustGeneratePrivateKey(clabernetesconstants.KeySize)

	caBytes, err := x509.CreateCertificate(cryptorand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("creating certificate authority certificate: %w", err)
	}

	caData, err := clabernetesutil.GenerateCertificateData(caBytes, caBytes, caKey)
	if err != nil {
		return fmt.Errorf("generating ca certificate: %w", err)
	}

	secret.Data["ca-ca.crt"] = caData.CRT
	secret.Data["ca-tls.crt"] = caData.TLS
	secret.Data["ca-tls.key"] = caData.Key

	// client
	clientCert := clabernetesutil.CreateClientCertificate("client")

	clientCertKey := clabernetesutil.MustGeneratePrivateKey(clabernetesconstants.KeySize)

	clientCertBytes, err := x509.CreateCertificate(
		cryptorand.Reader,
		clientCert,
		ca,
		&clientCertKey.PublicKey,
		caKey,
	)
	if err != nil {
		return fmt.Errorf("creating client certificate: %w", err)
	}

	clientData, err := clabernetesutil.GenerateCertificateData(
		clientCertBytes,
		caBytes,
		clientCertKey,
	)
	if err != nil {
		return fmt.Errorf("generating client certificate: %w", err)
	}

	secret.Data["client-ca.crt"] = clientData.CRT
	secret.Data["client-tls.crt"] = clientData.TLS
	secret.Data["client-tls.key"] = clientData.Key

	// webhook
	webhookCert := clabernetesutil.CreateClientCertificate("webhook")
	webhookCert.DNSNames = []string{
		"localhost",
		fmt.Sprintf("%s-webhook.%s.svc", c.GetAppName(), c.GetNamespace()),
	}

	webhookCertKey := clabernetesutil.MustGeneratePrivateKey(clabernetesconstants.KeySize)

	webhookCertBytes, err := x509.CreateCertificate(
		cryptorand.Reader,
		webhookCert,
		ca,
		&webhookCertKey.PublicKey,
		caKey,
	)
	if err != nil {
		return fmt.Errorf("creating webhook certificate: %w", err)
	}

	webhookData, err := clabernetesutil.GenerateCertificateData(
		webhookCertBytes,
		caBytes,
		webhookCertKey,
	)
	if err != nil {
		return fmt.Errorf("generating webhook certificate: %w", err)
	}

	secret.Data["webhook-ca.crt"] = webhookData.CRT
	secret.Data["webhook-tls.crt"] = webhookData.TLS
	secret.Data["webhook-tls.key"] = webhookData.Key

	err = updateCertificateSecret(c, secret)
	if err != nil {
		return fmt.Errorf("updating certificate secret: %w", err)
	}

	return nil
}

func updateCertificateSecret(
	c clabernetesmanagertypes.Clabernetes,
	secret *k8scorev1.Secret,
) error {
	client := c.GetKubeClient()

	ctx, ctxCancel := c.NewContextWithTimeout()
	defer ctxCancel()

	_, err := client.CoreV1().Secrets(c.GetNamespace()).Update(
		ctx,
		secret,
		metav1.UpdateOptions{},
	)

	return err
}
