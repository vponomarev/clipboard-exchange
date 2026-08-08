package config

import (
	"errors"
	"flag"
	"time"
)

type Config struct {
	Listen          string
	DatabasePath    string
	TLSCert         string
	TLSKey          string
	RoomTTL         time.Duration
	MaxItemBytes    int64
	MaxItemsPerRoom int
	MaxRooms        int
	RateLimit       int
	TrustProxy      bool
}

func Default() Config {
	return Config{
		Listen:          ":8080",
		DatabasePath:    "clipboard-exchange.db",
		RoomTTL:         30 * 24 * time.Hour,
		MaxItemBytes:    64 << 10,
		MaxItemsPerRoom: 500,
		MaxRooms:        10_000,
		RateLimit:       120,
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
	fs.BoolVar(&cfg.TrustProxy, "trust-proxy", false, "trust Forwarded and X-Forwarded-* headers")
}

func (c Config) Validate() error {
	if c.Listen == "" || c.DatabasePath == "" {
		return errors.New("listen and database must not be empty")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("tls-cert and tls-key must be specified together")
	}
	if c.RoomTTL < 0 || c.MaxItemBytes < 1 || c.MaxItemsPerRoom < 1 || c.MaxRooms < 1 || c.RateLimit < 0 {
		return errors.New("limits must be positive (rate-limit and room-ttl may be zero)")
	}
	return nil
}
