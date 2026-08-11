package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Listen             string
	DatabasePath       string
	TLSCert            string
	TLSKey             string
	RoomTTL            time.Duration
	MaxItemBytes       int64
	MaxItemsPerRoom    int
	MaxRooms           int
	RateLimit          int
	ShortLinkRateLimit int
	MaxShortLinks      int
	TrustProxy         bool
	FilesDir           string
	MaxFileBytes       int64
	MaxRoomFileBytes   int64
	FileChunkBytes     int64
	UploadTTL          time.Duration
	MaxActiveUploads   int
}

func Default() Config {
	return Config{
		Listen:             ":8080",
		DatabasePath:       "clipboard-exchange.db",
		RoomTTL:            30 * 24 * time.Hour,
		MaxItemBytes:       64 << 10,
		MaxItemsPerRoom:    500,
		MaxRooms:           10_000,
		RateLimit:          120,
		ShortLinkRateLimit: 30,
		MaxShortLinks:      10_000,
		FilesDir:           "clipboard-exchange-files",
		MaxFileBytes:       500 << 20,
		MaxRoomFileBytes:   500 << 20,
		FileChunkBytes:     1 << 20,
		UploadTTL:          24 * time.Hour,
		MaxActiveUploads:   32,
	}
}

func BindFlags(fs *flag.FlagSet, cfg *Config) {
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "HTTP listen address")
	fs.StringVar(&cfg.DatabasePath, "database", cfg.DatabasePath, "SQLite database path")
	fs.StringVar(&cfg.TLSCert, "tls-cert", "", "TLS certificate PEM file")
	fs.StringVar(&cfg.TLSKey, "tls-key", "", "TLS private key PEM file")
	fs.DurationVar(&cfg.RoomTTL, "room-ttl", cfg.RoomTTL, "delete rooms inactive for this duration (0 disables)")
	fs.Int64Var(&cfg.MaxItemBytes, "max-item-bytes", cfg.MaxItemBytes, "maximum UTF-8 text or encrypted envelope size")
	fs.IntVar(&cfg.MaxItemsPerRoom, "max-items-per-room", cfg.MaxItemsPerRoom, "maximum items retained per room")
	fs.IntVar(&cfg.MaxRooms, "max-rooms", cfg.MaxRooms, "maximum rooms")
	fs.IntVar(&cfg.RateLimit, "rate-limit", cfg.RateLimit, "mutating requests per IP per minute (0 disables)")
	fs.IntVar(&cfg.ShortLinkRateLimit, "short-link-rate-limit", cfg.ShortLinkRateLimit, "short-link retrieval requests per IP per minute (0 disables)")
	fs.IntVar(&cfg.MaxShortLinks, "max-short-links", cfg.MaxShortLinks, "maximum active short links")
	fs.BoolVar(&cfg.TrustProxy, "trust-proxy", false, "trust Forwarded and X-Forwarded-* headers")
	fs.StringVar(&cfg.FilesDir, "files-dir", cfg.FilesDir, "directory for uploaded files and temporary chunks")
	fs.Int64Var(&cfg.MaxFileBytes, "max-file-bytes", cfg.MaxFileBytes, "maximum stored bytes per file")
	fs.Int64Var(&cfg.MaxRoomFileBytes, "max-room-file-bytes", cfg.MaxRoomFileBytes, "maximum stored file bytes per room, including reservations")
	fs.Int64Var(&cfg.FileChunkBytes, "file-chunk-bytes", cfg.FileChunkBytes, "negotiated upload chunk size")
	fs.DurationVar(&cfg.UploadTTL, "upload-ttl", cfg.UploadTTL, "expiration time for incomplete uploads")
	fs.IntVar(&cfg.MaxActiveUploads, "max-active-uploads", cfg.MaxActiveUploads, "maximum active uploads server-wide")
}

func ApplyEnvironment(cfg *Config) error {
	setString := func(name string, target *string) {
		if value, ok := os.LookupEnv(name); ok {
			*target = value
		}
	}
	setInt := func(name string, target *int) error {
		value, ok := os.LookupEnv(name)
		if !ok {
			return nil
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		*target = parsed
		return nil
	}
	setInt64 := func(name string, target *int64) error {
		value, ok := os.LookupEnv(name)
		if !ok {
			return nil
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		*target = parsed
		return nil
	}

	setString("CLIPBOARD_EXCHANGE_LISTEN", &cfg.Listen)
	setString("CLIPBOARD_EXCHANGE_DATABASE", &cfg.DatabasePath)
	setString("CLIPBOARD_EXCHANGE_TLS_CERT", &cfg.TLSCert)
	setString("CLIPBOARD_EXCHANGE_TLS_KEY", &cfg.TLSKey)
	setString("CLIPBOARD_EXCHANGE_FILES_DIR", &cfg.FilesDir)
	if value, ok := os.LookupEnv("CLIPBOARD_EXCHANGE_ROOM_TTL"); ok {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("CLIPBOARD_EXCHANGE_ROOM_TTL: %w", err)
		}
		cfg.RoomTTL = parsed
	}
	if err := setInt64("CLIPBOARD_EXCHANGE_MAX_ITEM_BYTES", &cfg.MaxItemBytes); err != nil {
		return err
	}
	if err := setInt("CLIPBOARD_EXCHANGE_MAX_ITEMS_PER_ROOM", &cfg.MaxItemsPerRoom); err != nil {
		return err
	}
	if err := setInt("CLIPBOARD_EXCHANGE_MAX_ROOMS", &cfg.MaxRooms); err != nil {
		return err
	}
	if err := setInt("CLIPBOARD_EXCHANGE_RATE_LIMIT", &cfg.RateLimit); err != nil {
		return err
	}
	if err := setInt("CLIPBOARD_EXCHANGE_SHORT_LINK_RATE_LIMIT", &cfg.ShortLinkRateLimit); err != nil {
		return err
	}
	if err := setInt("CLIPBOARD_EXCHANGE_MAX_SHORT_LINKS", &cfg.MaxShortLinks); err != nil {
		return err
	}
	if err := setInt64("CLIPBOARD_EXCHANGE_MAX_FILE_BYTES", &cfg.MaxFileBytes); err != nil {
		return err
	}
	if err := setInt64("CLIPBOARD_EXCHANGE_MAX_ROOM_FILE_BYTES", &cfg.MaxRoomFileBytes); err != nil {
		return err
	}
	if err := setInt64("CLIPBOARD_EXCHANGE_FILE_CHUNK_BYTES", &cfg.FileChunkBytes); err != nil {
		return err
	}
	if err := setInt("CLIPBOARD_EXCHANGE_MAX_ACTIVE_UPLOADS", &cfg.MaxActiveUploads); err != nil {
		return err
	}
	if value, ok := os.LookupEnv("CLIPBOARD_EXCHANGE_UPLOAD_TTL"); ok {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("CLIPBOARD_EXCHANGE_UPLOAD_TTL: %w", err)
		}
		cfg.UploadTTL = parsed
	}
	if value, ok := os.LookupEnv("CLIPBOARD_EXCHANGE_TRUST_PROXY"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("CLIPBOARD_EXCHANGE_TRUST_PROXY: %w", err)
		}
		cfg.TrustProxy = parsed
	}
	return nil
}

func (c Config) Validate() error {
	if c.Listen == "" || c.DatabasePath == "" || c.FilesDir == "" {
		return errors.New("listen, database, and files-dir must not be empty")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("tls-cert and tls-key must be specified together")
	}
	if c.RoomTTL < 0 || c.MaxItemBytes < 1 || c.MaxItemsPerRoom < 1 || c.MaxRooms < 1 || c.RateLimit < 0 || c.ShortLinkRateLimit < 0 || c.MaxShortLinks < 1 || c.MaxFileBytes < 1 || c.MaxRoomFileBytes < 1 || c.FileChunkBytes < 1 || c.FileChunkBytes > c.MaxFileBytes || c.UploadTTL <= 0 || c.MaxActiveUploads < 1 {
		return errors.New("limits must be positive (rate-limit and room-ttl may be zero)")
	}
	return nil
}
