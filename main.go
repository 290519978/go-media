package main

import (
	"context"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"maas-box/internal/config"
	"maas-box/internal/logutil"
	"maas-box/internal/server"
)

var loadEmbeddedWebFSHook = func() fs.FS {
	return nil
}

func main() {
	configPath := flag.String("config", "./configs/prod/config.toml", "path to config.toml")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}
	logutil.SetLevel(cfg.Log.Level)

	webFS := loadEmbeddedWebFSHook()
	if webFS != nil {
		logutil.Infof("embedded web assets enabled")
	}
	app, err := server.New(cfg, server.WithWebFS(webFS))
	if err != nil {
		log.Fatalf("init server failed: %v", err)
	}

	engine := app.Engine()
	addr := ":" + strconv.Itoa(cfg.Server.HTTP.Port)
	httpTimeout, err := time.ParseDuration(cfg.Server.HTTP.Timeout)
	if err != nil || httpTimeout <= 0 {
		httpTimeout = 60 * time.Second
	}

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      engine,
		ReadTimeout:  httpTimeout,
		WriteTimeout: httpTimeout,
		IdleTimeout:  90 * time.Second,
	}

	go func() {
		logutil.Infof("maas-box server started: http://127.0.0.1%s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	if err := app.Close(); err != nil {
		logutil.Warnf("close app error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logutil.Warnf("shutdown error: %v", err)
	}
}
