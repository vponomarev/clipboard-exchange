package config

import "testing"

func TestValidateTLSIsPair(t *testing.T) {
	cfg := Default()
	cfg.TLSCert = "cert.pem"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for certificate without key")
	}
	cfg.TLSKey = "key.pem"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateLimits(t *testing.T) {
	cfg := Default()
	cfg.MaxItemsPerRoom = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero item limit")
	}
}
