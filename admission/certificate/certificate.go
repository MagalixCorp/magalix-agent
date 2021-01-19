package certificate

import (
	"bytes"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"os"
	"time"
)

var dnsNames = []string{"host.minikube.internal"}

const commonName = "magalix-CA"

func Generate() ([]byte, error) {
	var caPEM, serverCertPEM, serverPrivKeyPEM *bytes.Buffer
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(0),
		Subject: pkix.Name{
			Organization: []string{"magalix.com"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caPrivKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	caCert, err := x509.CreateCertificate(cryptorand.Reader, caTmpl, caTmpl, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return nil, err
	}

	caPEM = &bytes.Buffer{}
	err = pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caCert,
	})
	if err != nil {
		return nil, err
	}

	serverTmpl := &x509.Certificate{
		DNSNames:     dnsNames,
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"magalix.com"},
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	serverPrivKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	serverCert, err := x509.CreateCertificate(cryptorand.Reader, serverTmpl, caTmpl, &serverPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return nil, err
	}

	serverCertPEM = &bytes.Buffer{}
	err = pem.Encode(serverCertPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverCert,
	})
	if err != nil {
		return nil, err
	}

	serverPrivKeyPEM = &bytes.Buffer{}
	err = pem.Encode(serverPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverPrivKey),
	})
	if err != nil {
		return nil, err
	}

	err = write("tls.crt", serverCertPEM)
	if err != nil {
		log.Panic(err)
	}

	err = write("tls.key", serverPrivKeyPEM)
	if err != nil {
		log.Panic(err)
	}
	return caPEM.Bytes(), nil
}

func write(filepath string, buff *bytes.Buffer) error {
	f, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(buff.Bytes())
	if err != nil {
		return err
	}
	return nil
}
