package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ryanycheng/Syrogo/internal/app"
	"github.com/ryanycheng/Syrogo/internal/config"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	configPath := flag.String("config", "./configs/config.example.yaml", "path to config file")
	devLog := flag.Bool("dev-log", false, "write logs to stdout and ./tmp/dev.log for local development")
	flag.Parse()

	logger, closeLogger, err := newLogger(newLoggerOptions{enableDevLog: *devLog})
	if err != nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})))
		slog.Error("initialize logger failed", slog.Any("error", err))
		return 1
	}
	defer closeLogger()
	slog.SetDefault(logger)

	if *devLog {
		slog.Info("development log enabled", slog.String("path", devLogPath))
	}

	if err := run(*configPath); err != nil {
		slog.Error("application exited with error", slog.Any("error", err))
		return 1
	}
	return 0
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	application, err := app.New(cfg)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server starting", "listen", cfg.ListenAddress())
		errCh <- application.Server.Start()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		slog.Info("shutdown signal received", "signal", sig.String())
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := application.Server.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}
