package test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"log"
	"testing"
)

func NewEncodedCertificateRequest(t *testing.T, csr *x509.CertificateRequest) []byte {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csr, privateKey)
	if err != nil {
		log.Fatalf("CreateCertificateRequest: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})
}
