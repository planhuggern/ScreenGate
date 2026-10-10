package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func run() error {
	configuration, err := loadConfiguration()
	if err != nil {
		return err
	}
	time.Local = configuration.location
	if err := os.MkdirAll(filepath.Dir(configuration.databasePath), 0700); err != nil {
		return err
	}
	repository, err := openRepository(configuration.databasePath)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer repository.close()
	app := newApplication(repository)
	app.adminPath, app.adminUser, app.adminPassword = configuration.adminPath, configuration.adminUser, configuration.adminPassword
	server := &http.Server{Addr: configuration.listenAddr, Handler: app.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("ScreenGate listening on %s (timezone %s)", server.Addr, time.Local)
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErrors:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func main() {
	if err := run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
