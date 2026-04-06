package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"syrogo/internal/app"
	"syrogo/internal/config"
)

func main() {
	configPath := flag.String("config", "./configs/config.example.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("build app: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("syrogo listening on %s", cfg.ListenAddress())
		errCh <- application.Server.Start()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("received signal %s, shutting down", sig)
	case err := <-errCh:
		if err != nil {
			log.Fatalf("server error: %v", err)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := application.Server.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown server: %v", err)
	}
}
