package systemd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vponomarev/clipboard-exchange/internal/config"
)

const (
	serviceName = "clipboard-exchange.service"
	serviceUser = "clipboard-exchange"
	binaryPath  = "/usr/local/bin/clipboard-exchange"
	unitPath    = "/etc/systemd/system/clipboard-exchange.service"
	configDir   = "/etc/clipboard-exchange"
	configPath  = "/etc/clipboard-exchange/clipboard-exchange.env"
	dataDir     = "/var/lib/clipboard-exchange"
)

func IsCommand(value string) bool {
	return value == "install" || value == "upgrade" || value == "deinstall"
}

func Run(args []string, version string) error {
	if len(args) == 0 || !IsCommand(args[0]) {
		return errors.New("expected install, upgrade, or deinstall")
	}
	if err := requireRoot(); err != nil {
		return err
	}
	switch args[0] {
	case "install":
		return install(args[1:], version)
	case "upgrade":
		return upgrade(args[1:], version)
	default:
		return deinstall(args[1:])
	}
}

func install(args []string, version string) error {
	cfg := config.Default()
	cfg.DatabasePath = filepath.Join(dataDir, "data.db")
	cfg.FilesDir = filepath.Join(dataDir, "files")
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config.BindFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("install options: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected install argument %q", fs.Arg(0))
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("install configuration: %w", err)
	}
	if filepath.Clean(cfg.DatabasePath) != filepath.Join(dataDir, "data.db") {
		return fmt.Errorf("systemd installation requires database path %s", filepath.Join(dataDir, "data.db"))
	}
	if filepath.Clean(cfg.FilesDir) != filepath.Join(dataDir, "files") {
		return fmt.Errorf("systemd installation requires files directory %s", filepath.Join(dataDir, "files"))
	}
	if _, err := os.Stat(binaryPath); err == nil {
		return errors.New("already installed; use a newly downloaded binary with the upgrade command")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ensureUserAndDirectories(); err != nil {
		return err
	}
	if err := copyCurrentExecutable(binaryPath); err != nil {
		return err
	}
	if err := writeAtomic(unitPath, []byte(unitFile()), 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		if err := writeAtomic(configPath, []byte(environmentFile(cfg)), 0o640); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := command("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := command("systemctl", "enable", "--now", serviceName); err != nil {
		return err
	}
	fmt.Printf("installed clipboard-exchange %s; configuration: %s; data: %s\n", version, configPath, dataDir)
	return nil
}

func upgrade(args []string, version string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("upgrade options: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected upgrade argument %q", fs.Arg(0))
	}
	if _, err := os.Stat(binaryPath); err != nil {
		return errors.New("not installed; use install first")
	}
	same, err := currentExecutableIs(binaryPath)
	if err != nil {
		return err
	}
	if same {
		return errors.New("upgrade must be run from the newly downloaded binary, not the installed binary")
	}
	backup := binaryPath + ".previous"
	if err := copyFile(binaryPath, backup, 0o755); err != nil {
		return fmt.Errorf("backup installed binary: %w", err)
	}
	rollback := func(cause error) error {
		rollbackErr := copyFile(backup, binaryPath, 0o755)
		if rollbackErr == nil {
			rollbackErr = command("systemctl", "daemon-reload")
		}
		if rollbackErr == nil {
			rollbackErr = command("systemctl", "restart", serviceName)
		}
		return fmt.Errorf("upgrade failed: %v; rollback: %v", cause, rollbackErr)
	}
	if err := copyCurrentExecutable(binaryPath); err != nil {
		return err
	}
	if err := writeAtomic(unitPath, []byte(unitFile()), 0o644); err != nil {
		return rollback(err)
	}
	if err := addFileStorageEnvironment(); err != nil {
		return rollback(err)
	}
	if err := command("systemctl", "daemon-reload"); err != nil {
		return rollback(err)
	}
	if err := command("systemctl", "restart", serviceName); err != nil {
		return rollback(err)
	}
	fmt.Printf("upgraded clipboard-exchange to %s; previous binary: %s\n", version, backup)
	return nil
}

func deinstall(args []string) error {
	fs := flag.NewFlagSet("deinstall", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	purge := fs.Bool("purge", false, "also remove configuration, database, and service user")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("deinstall options: %w", err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected deinstall argument %q", fs.Arg(0))
	}
	stopErr := command("systemctl", "disable", "--now", serviceName)
	for _, path := range []string{unitPath, binaryPath, binaryPath + ".previous"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := command("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if *purge {
		if err := os.RemoveAll(configDir); err != nil {
			return err
		}
		if err := os.RemoveAll(dataDir); err != nil {
			return err
		}
		if err := command("userdel", serviceUser); err != nil {
			return fmt.Errorf("remove service user: %w", err)
		}
		fmt.Println("deinstalled clipboard-exchange and purged configuration and data")
	} else {
		fmt.Printf("deinstalled clipboard-exchange; preserved configuration in %s and data in %s\n", configDir, dataDir)
	}
	if stopErr != nil {
		return fmt.Errorf("service files removed, but stopping the service reported: %w", stopErr)
	}
	return nil
}

func ensureUserAndDirectories() error {
	if err := command("id", "-u", serviceUser); err != nil {
		if err := command("useradd", "--system", "--home-dir", dataDir, "--shell", "/usr/sbin/nologin", serviceUser); err != nil {
			return fmt.Errorf("create service user: %w", err)
		}
	}
	if err := command("install", "-d", "-m", "0750", "-o", "root", "-g", serviceUser, configDir); err != nil {
		return err
	}
	if err := command("install", "-d", "-m", "0750", "-o", serviceUser, "-g", serviceUser, dataDir); err != nil {
		return err
	}
	return nil
}

func unitFile() string {
	return `[Unit]
Description=Clipboard Exchange
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=clipboard-exchange
Group=clipboard-exchange
EnvironmentFile=-/etc/clipboard-exchange/clipboard-exchange.env
ExecStart=/usr/local/bin/clipboard-exchange
WorkingDirectory=/var/lib/clipboard-exchange
Restart=on-failure
RestartSec=2s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/clipboard-exchange
CapabilityBoundingSet=
AmbientCapabilities=
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX

[Install]
WantedBy=multi-user.target
`
}

func environmentFile(cfg config.Config) string {
	values := [][2]string{
		{"CLIPBOARD_EXCHANGE_LISTEN", cfg.Listen},
		{"CLIPBOARD_EXCHANGE_DATABASE", cfg.DatabasePath},
		{"CLIPBOARD_EXCHANGE_TLS_CERT", cfg.TLSCert},
		{"CLIPBOARD_EXCHANGE_TLS_KEY", cfg.TLSKey},
		{"CLIPBOARD_EXCHANGE_ROOM_TTL", cfg.RoomTTL.String()},
		{"CLIPBOARD_EXCHANGE_MAX_ITEM_BYTES", strconv.FormatInt(cfg.MaxItemBytes, 10)},
		{"CLIPBOARD_EXCHANGE_MAX_ITEMS_PER_ROOM", strconv.Itoa(cfg.MaxItemsPerRoom)},
		{"CLIPBOARD_EXCHANGE_MAX_ROOMS", strconv.Itoa(cfg.MaxRooms)},
		{"CLIPBOARD_EXCHANGE_RATE_LIMIT", strconv.Itoa(cfg.RateLimit)},
		{"CLIPBOARD_EXCHANGE_SHORT_LINK_RATE_LIMIT", strconv.Itoa(cfg.ShortLinkRateLimit)},
		{"CLIPBOARD_EXCHANGE_MAX_SHORT_LINKS", strconv.Itoa(cfg.MaxShortLinks)},
		{"CLIPBOARD_EXCHANGE_TRUST_PROXY", strconv.FormatBool(cfg.TrustProxy)},
		{"CLIPBOARD_EXCHANGE_FILES_DIR", cfg.FilesDir},
		{"CLIPBOARD_EXCHANGE_MAX_FILE_BYTES", strconv.FormatInt(cfg.MaxFileBytes, 10)},
		{"CLIPBOARD_EXCHANGE_MAX_ROOM_FILE_BYTES", strconv.FormatInt(cfg.MaxRoomFileBytes, 10)},
		{"CLIPBOARD_EXCHANGE_FILE_CHUNK_BYTES", strconv.FormatInt(cfg.FileChunkBytes, 10)},
		{"CLIPBOARD_EXCHANGE_UPLOAD_TTL", cfg.UploadTTL.String()},
		{"CLIPBOARD_EXCHANGE_MAX_ACTIVE_UPLOADS", strconv.Itoa(cfg.MaxActiveUploads)},
	}
	var out strings.Builder
	out.WriteString("# Managed initially by clipboard-exchange install; safe to edit.\n")
	for _, value := range values {
		fmt.Fprintf(&out, "%s=%s\n", value[0], strconv.Quote(value[1]))
	}
	return out.String()
}

func addFileStorageEnvironment() error {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	cfg := config.Default()
	cfg.FilesDir = filepath.Join(dataDir, "files")
	values := [][2]string{
		{"CLIPBOARD_EXCHANGE_SHORT_LINK_RATE_LIMIT", strconv.Itoa(cfg.ShortLinkRateLimit)},
		{"CLIPBOARD_EXCHANGE_MAX_SHORT_LINKS", strconv.Itoa(cfg.MaxShortLinks)},
		{"CLIPBOARD_EXCHANGE_FILES_DIR", cfg.FilesDir},
		{"CLIPBOARD_EXCHANGE_MAX_FILE_BYTES", strconv.FormatInt(cfg.MaxFileBytes, 10)},
		{"CLIPBOARD_EXCHANGE_MAX_ROOM_FILE_BYTES", strconv.FormatInt(cfg.MaxRoomFileBytes, 10)},
		{"CLIPBOARD_EXCHANGE_FILE_CHUNK_BYTES", strconv.FormatInt(cfg.FileChunkBytes, 10)},
		{"CLIPBOARD_EXCHANGE_UPLOAD_TTL", cfg.UploadTTL.String()},
		{"CLIPBOARD_EXCHANGE_MAX_ACTIVE_UPLOADS", strconv.Itoa(cfg.MaxActiveUploads)},
	}
	text := string(content)
	for _, value := range values {
		if !strings.Contains(text, "\n"+value[0]+"=") && !strings.HasPrefix(text, value[0]+"=") {
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			text += value[0] + "=" + strconv.Quote(value[1]) + "\n"
		}
	}
	return writeAtomic(configPath, []byte(text), 0o640)
}

func command(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func copyCurrentExecutable(destination string) error {
	source, err := os.Executable()
	if err != nil {
		return err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	return copyFile(source, destination, 0o755)
}

func currentExecutableIs(path string) (bool, error) {
	current, err := os.Executable()
	if err != nil {
		return false, err
	}
	a, err := os.Stat(current)
	if err != nil {
		return false, err
	}
	b, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return os.SameFile(a, b), nil
}

func copyFile(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	dir := filepath.Dir(destination)
	tmp, err := os.CreateTemp(dir, ".clipboard-exchange-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, destination)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".clipboard-exchange-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
