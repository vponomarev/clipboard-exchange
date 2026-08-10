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

	"github.com/vponomarev/clipboard-exchange/internal/admin"
	"github.com/vponomarev/clipboard-exchange/internal/config"
	"github.com/vponomarev/clipboard-exchange/internal/filestore"
	"github.com/vponomarev/clipboard-exchange/internal/httpserver"
	"github.com/vponomarev/clipboard-exchange/internal/store"
	"github.com/vponomarev/clipboard-exchange/internal/systemd"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && systemd.IsCommand(os.Args[1]) {
		if err := systemd.Run(os.Args[1:], version); err != nil {
			log.Printf("systemd: %v", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && admin.IsCommand(os.Args[1]) {
		if err := admin.Run(os.Args[1:], version); err != nil {
			log.Printf("admin: %v", err)
			os.Exit(1)
		}
		return
	}

	cfg := config.Default()
	if err := config.ApplyEnvironment(&cfg); err != nil {
		log.Printf("invalid environment configuration: %v", err)
		os.Exit(2)
	}
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
	files, err := filestore.Open(cfg.FilesDir)
	if err != nil {
		log.Printf("open file storage: %v", err)
		os.Exit(1)
	}

	handler := httpserver.New(cfg, db, files, log.Default())
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
	go runUploadCleanup(ctx, db, files, cfg.RoomTTL, cfg.MaxFileBytes)

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

func runUploadCleanup(ctx context.Context, db *store.Store, files *filestore.Store, roomTTL time.Duration, warningBytes int64) {
	cleanup := func() {
		if _, err := db.DeleteExpiredRooms(ctx, time.Now(), roomTTL); err != nil {
			log.Printf("cleanup expired rooms: %v", err)
		}
		expiredEntries, err := db.DeleteExpiredEntries(ctx, time.Now())
		if err != nil {
			log.Printf("cleanup expired entries: %v", err)
		} else {
			for _, id := range expiredEntries.FileIDs {
				if err := files.RemoveObject(id); err != nil {
					log.Printf("cleanup expired entry object %s: %v", id, err)
				}
			}
			for _, id := range expiredEntries.UploadIDs {
				if err := files.RemoveUpload(id); err != nil {
					log.Printf("cleanup expired entry upload %s: %v", id, err)
				}
			}
		}
		ids, err := db.DeleteExpiredUploads(ctx, time.Now())
		if err != nil {
			log.Printf("cleanup expired uploads: %v", err)
			return
		}
		for _, id := range ids {
			if err := files.RemoveUpload(id); err != nil {
				log.Printf("cleanup upload %s: %v", id, err)
			}
		}
		activeUploads, activeObjects, err := db.StorageReferences(ctx)
		if err != nil {
			log.Printf("read file storage references: %v", err)
			return
		}
		if err := files.Reconcile(activeUploads, activeObjects); err != nil {
			log.Printf("reconcile file storage: %v", err)
		}
		if available, total, err := files.DiskSpace(); err == nil && (available < uint64(warningBytes) || (total > 0 && available < total/20)) {
			log.Printf("WARNING: file storage disk space is low available_bytes=%d total_bytes=%d", available, total)
		}
	}
	cleanup()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}
