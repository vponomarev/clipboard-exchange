package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vponomarev/clipboard-exchange/internal/config"
	"github.com/vponomarev/clipboard-exchange/internal/httpserver"
	"github.com/vponomarev/clipboard-exchange/internal/store"
)

var version = "dev"

func main() {
	cfg := config.Default()
	config.BindFlags(flag.CommandLine, &cfg)
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if err := cfg.Validate(); err != nil {
		log.Printf("invalid configuration: %v", err)
		os.Exit(2)
	}

	db, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Printf("open database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	handler := httpserver.New(cfg, db, log.Default())
	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go db.RunCleanup(ctx, cfg.RoomTTL, time.Hour)

	errCh := make(chan error, 1)
	go func() {
		log.Printf("clipboard-exchange listening address=%s version=%s tls=%t", cfg.Listen, version, cfg.TLSCert != "")
		if cfg.TLSCert != "" {
			errCh <- httpServer.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			errCh <- httpServer.ListenAndServe()
		}
	}()

	select {
	case <-ctx.Done():
		log.Printf("shutting down")
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server stopped: %v", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown: %v", err)
	}
}
