package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ryanycheng/Syrogo/internal/app"
	"github.com/ryanycheng/Syrogo/internal/config"
)

var version = "dev"

const startupWordmark = `   ____                   ____
  / ___| _   _ _ __ ___  / ___| ___
  \___ \| | | | '__/ _ \| |  _ / _ \
   ___) | |_| | | | (_) | |_| | (_) |
  |____/ \__, |_|  \___/ \____|\___/
         |___/`

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
	defer func() {
		if err := closeLogger(); err != nil {
			slog.Error("close logger failed", slog.Any("error", err))
		}
	}()
	slog.SetDefault(logger)

	if err := run(*configPath, *devLog); err != nil {
		slog.Error("application exited with error", slog.Any("error", err))
		return 1
	}
	return 0
}

func run(configPath string, devLogEnabled bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	application, err := app.New(cfg)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(os.Stdout, buildStartupBanner(startupBannerData{
		Version:       version,
		Tagline:       "AI Gateway / Semantic Router",
		Listens:       cfg.ListenAddresses(),
		DevLogEnabled: devLogEnabled,
		TraceMode:     os.Getenv("SYROGO_TRACE"),
	})); err != nil {
		return fmt.Errorf("write startup banner: %w", err)
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

type startupBannerData struct {
	Version       string
	Tagline       string
	Listens       []string
	DevLogEnabled bool
	TraceMode     string
}

func buildStartupBanner(data startupBannerData) string {
	versionText := data.Version
	if versionText == "" {
		versionText = "dev"
	}

	listenText := "(none)"
	if len(data.Listens) > 0 {
		listenText = strings.Join(data.Listens, ", ")
	}

	devLogText := "off"
	if data.DevLogEnabled {
		devLogText = fmt.Sprintf("on (%s)", devLogPath)
	}

	traceText := data.TraceMode
	if traceText == "" {
		traceText = "off"
	}

	return strings.Join([]string{
		startupWordmark,
		fmt.Sprintf("  %s", data.Tagline),
		fmt.Sprintf("  version: %s", versionText),
		fmt.Sprintf("  listen: %s", listenText),
		fmt.Sprintf("  dev-log: %s", devLogText),
		fmt.Sprintf("  trace: %s", traceText),
	}, "\n")
}
