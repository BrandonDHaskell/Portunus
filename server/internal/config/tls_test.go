package config

import (
	"crypto/x509"
	"testing"
)

func TestEphemeralCertUsable(t *testing.T) {
	cert, err := EphemeralCert()
	if err != nil {
		t.Fatalf("EphemeralCert: %v", err)
	}
	if len(cert.Certificate) == 0 || cert.PrivateKey == nil {
		t.Fatal("EphemeralCert returned an incomplete certificate")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if err := leaf.VerifyHostname("localhost"); err != nil {
		t.Errorf("cert does not cover localhost: %v", err)
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Errorf("cert does not cover 127.0.0.1: %v", err)
	}
}
