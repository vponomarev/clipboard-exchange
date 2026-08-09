package config

import (
	"strings"
	"testing"
	"time"
)

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

func TestApplyEnvironment(t *testing.T) {
	t.Setenv("CLIPBOARD_EXCHANGE_LISTEN", "127.0.0.1:9090")
	t.Setenv("CLIPBOARD_EXCHANGE_DATABASE", "/tmp/exchange.db")
	t.Setenv("CLIPBOARD_EXCHANGE_ROOM_TTL", "48h")
	t.Setenv("CLIPBOARD_EXCHANGE_MAX_ITEMS_PER_ROOM", "42")
	t.Setenv("CLIPBOARD_EXCHANGE_TRUST_PROXY", "true")
	t.Setenv("CLIPBOARD_EXCHANGE_FILES_DIR", "/tmp/exchange-files")
	t.Setenv("CLIPBOARD_EXCHANGE_MAX_ROOM_FILE_BYTES", "123456")
	t.Setenv("CLIPBOARD_EXCHANGE_UPLOAD_TTL", "2h")
	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9090" || cfg.DatabasePath != "/tmp/exchange.db" || cfg.RoomTTL != 48*time.Hour || cfg.MaxItemsPerRoom != 42 || !cfg.TrustProxy || cfg.FilesDir != "/tmp/exchange-files" || cfg.MaxRoomFileBytes != 123456 || cfg.UploadTTL != 2*time.Hour {
		t.Fatalf("unexpected environment config: %+v", cfg)
	}
}

func TestApplyEnvironmentRejectsInvalidValue(t *testing.T) {
	t.Setenv("CLIPBOARD_EXCHANGE_RATE_LIMIT", "many")
	cfg := Default()
	err := ApplyEnvironment(&cfg)
	if err == nil || !strings.Contains(err.Error(), "CLIPBOARD_EXCHANGE_RATE_LIMIT") {
		t.Fatalf("expected named parse error, got %v", err)
	}
}
