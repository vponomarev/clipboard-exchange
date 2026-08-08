package systemd

import (
	"strings"
	"testing"
	"time"

	"github.com/vponomarev/clipboard-exchange/internal/config"
)

func TestRecognizesLifecycleCommands(t *testing.T) {
	for _, command := range []string{"install", "upgrade", "deinstall"} {
		if !IsCommand(command) {
			t.Fatalf("command %q not recognized", command)
		}
	}
	if IsCommand("serve") {
		t.Fatal("unexpected command recognized")
	}
}

func TestUnitFileIsHardenedAndRestartable(t *testing.T) {
	unit := unitFile()
	for _, expected := range []string{
		"ExecStart=/usr/local/bin/clipboard-exchange",
		"EnvironmentFile=-/etc/clipboard-exchange/clipboard-exchange.env",
		"Restart=on-failure",
		"ProtectSystem=strict",
		"ReadWritePaths=/var/lib/clipboard-exchange",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, expected) {
			t.Errorf("unit does not contain %q", expected)
		}
	}
}

func TestEnvironmentFileContainsServerConfiguration(t *testing.T) {
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:8080"
	cfg.DatabasePath = "/var/lib/clipboard-exchange/data.db"
	cfg.FilesDir = "/var/lib/clipboard-exchange/files"
	cfg.RoomTTL = 48 * time.Hour
	cfg.TrustProxy = true
	env := environmentFile(cfg)
	for _, expected := range []string{
		`CLIPBOARD_EXCHANGE_LISTEN="127.0.0.1:8080"`,
		`CLIPBOARD_EXCHANGE_DATABASE="/var/lib/clipboard-exchange/data.db"`,
		`CLIPBOARD_EXCHANGE_ROOM_TTL="48h0m0s"`,
		`CLIPBOARD_EXCHANGE_TRUST_PROXY="true"`,
		`CLIPBOARD_EXCHANGE_FILES_DIR="/var/lib/clipboard-exchange/files"`,
		`CLIPBOARD_EXCHANGE_MAX_ROOM_FILE_BYTES="524288000"`,
	} {
		if !strings.Contains(env, expected) {
			t.Errorf("environment file does not contain %q", expected)
		}
	}
}
