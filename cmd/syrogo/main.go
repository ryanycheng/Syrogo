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
	internallogging "github.com/ryanycheng/Syrogo/internal/logging"
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
	args := os.Args[1:]
	if isVersionCommand(args) {
		if _, err := fmt.Fprintln(os.Stdout, formatVersion(version)); err != nil {
			return 1
		}
		return 0
	}
	if command, commandArgs, ok := splitCommandArgs(args); ok {
		switch command {
		case "run":
			return runLauncher(commandArgs)
		case "activate":
			return runActivate(commandArgs)
		case "session":
			return runSession(commandArgs)
		}
	}

	bootstrapLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	slog.SetDefault(bootstrapLogger)

	configPath := flag.String("config", "./configs/config.example.yaml", "path to config file")
	devLog := flag.Bool("dev-log", false, "write logs to stdout and the configured admin log path for local development")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config failed", slog.Any("error", err))
		return 1
	}
	loggerOpts := newLoggerOptions{enableDevLog: *devLog, logs: cfg.Admin.Logs}
	loggerResult, err := newLogger(loggerOpts)
	if err != nil {
		slog.Error("initialize logger failed", slog.Any("error", err))
		return 1
	}
	defer func() {
		if err := loggerResult.close(); err != nil {
			slog.Error("close logger failed", slog.Any("error", err))
		}
	}()
	slog.SetDefault(loggerResult.logger)

	if err := run(*configPath, cfg, *devLog, devLogPath(loggerOpts), loggerResult.recentLogs); err != nil {
		slog.Error("application exited with error", slog.Any("error", err))
		return 1
	}
	return 0
}

func isVersionCommand(args []string) bool {
	return len(args) == 1 && (args[0] == "--version" || args[0] == "-version" || args[0] == "version")
}

func formatVersion(buildVersion string) string {
	return fmt.Sprintf("syrogo %s", normalizeVersion(buildVersion))
}

func normalizeVersion(buildVersion string) string {
	if buildVersion == "" {
		return "dev"
	}
	return buildVersion
}

func splitCommandArgs(args []string) (string, []string, bool) {
	for index, arg := range args {
		if arg != "run" && arg != "activate" && arg != "session" {
			continue
		}

		commandArgs := append([]string(nil), args[index+1:]...)
		prefix := args[:index]
		for i := 0; i < len(prefix); i++ {
			switch prefix[i] {
			case "--config", "-config":
				if i+1 < len(prefix) {
					commandArgs = append([]string{"--config", prefix[i+1]}, commandArgs...)
					i++
				}
			default:
				if value, ok := strings.CutPrefix(prefix[i], "--config="); ok {
					commandArgs = append([]string{"--config", value}, commandArgs...)
				}
			}
		}
		return arg, commandArgs, true
	}
	return "", nil, false
}

func run(configPath string, cfg config.Config, devLogEnabled bool, logPath string, recentLogs *internallogging.RecentBuffer) error {
	application, err := app.NewWithOptions(cfg, app.Options{ConfigPath: configPath, RecentLogs: recentLogs})
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(os.Stdout, buildStartupBanner(startupBannerData{
		Version:       version,
		Tagline:       "AI Gateway / Semantic Router",
		Listens:       cfg.ListenAddresses(),
		DevLogEnabled: devLogEnabled,
		DevLogPath:    logPath,
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

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	shutdownErr := application.Server.Shutdown(shutdownCtx)
	cancelShutdown()

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
	closeErr := application.Close(closeCtx)
	cancelClose()
	if shutdownErr != nil {
		return shutdownErr
	}
	return closeErr
}

type startupBannerData struct {
	Version       string
	Tagline       string
	Listens       []string
	DevLogEnabled bool
	DevLogPath    string
	TraceMode     string
}

func buildStartupBanner(data startupBannerData) string {
	versionText := normalizeVersion(data.Version)

	listenText := "(none)"
	if len(data.Listens) > 0 {
		listenText = strings.Join(data.Listens, ", ")
	}

	devLogText := "off"
	if data.DevLogEnabled {
		devLogText = fmt.Sprintf("on (%s)", data.DevLogPath)
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
