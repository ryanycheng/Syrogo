package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
)

func testAdminLogsConfig(path string) config.AdminLogsConfig {
	return config.AdminLogsConfig{
		Path: path,
		Rotation: config.AdminLogsRotationConfig{
			MaxSizeMB:      1,
			MaxFiles:       2,
			MaxAgeDays:     1,
			MaxTotalSizeMB: 2,
		},
	}
}

func TestNewLoggerWithDevLogWritesStdoutAndFile(t *testing.T) {
	baseDir := t.TempDir()
	var stdout bytes.Buffer

	result, err := newLogger(newLoggerOptions{
		enableDevLog: true,
		stdout:       &stdout,
		baseDir:      baseDir,
		logs:         testAdminLogsConfig("var/custom.log"),
	})
	if err != nil {
		t.Fatalf("newLogger() error = %v", err)
	}
	defer func() {
		if err := result.close(); err != nil {
			t.Fatalf("closeFn() error = %v", err)
		}
	}()

	if result.recentLogs == nil {
		t.Fatal("recentLogs = nil with dev log enabled")
	}
	result.logger.Info("hello dev log", slog.String("component", "test"))

	gotStdout := stdout.String()
	if !strings.Contains(gotStdout, "hello dev log") {
		t.Fatalf("stdout logs = %q, want hello dev log", gotStdout)
	}

	logFilePath := filepath.Join(baseDir, "var/custom.log")
	content, err := os.ReadFile(logFilePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", logFilePath, err)
	}
	gotFile := string(content)
	if !strings.Contains(gotFile, "hello dev log") || !strings.Contains(gotFile, "component=test") {
		t.Fatalf("file logs = %q, want hello dev log with component", gotFile)
	}
}

func TestNewLoggerWithoutDevLogDoesNotCreateFile(t *testing.T) {
	baseDir := t.TempDir()
	var stdout bytes.Buffer

	result, err := newLogger(newLoggerOptions{
		enableDevLog: false,
		stdout:       &stdout,
		baseDir:      baseDir,
		logs:         testAdminLogsConfig("var/custom.log"),
	})
	if err != nil {
		t.Fatalf("newLogger() error = %v", err)
	}
	defer func() {
		if err := result.close(); err != nil {
			t.Fatalf("closeFn() error = %v", err)
		}
	}()

	if result.recentLogs != nil {
		t.Fatal("recentLogs != nil without dev log")
	}
	result.logger.Info("stdout only")

	if !strings.Contains(stdout.String(), "stdout only") {
		t.Fatalf("stdout logs = %q, want stdout only", stdout.String())
	}

	if _, err := os.Stat(filepath.Join(baseDir, "var/custom.log")); !os.IsNotExist(err) {
		t.Fatalf("dev log file existence error = %v, want not exists", err)
	}
}

func TestNewLoggerUsesConfiguredRotation(t *testing.T) {
	baseDir := t.TempDir()
	var stdout bytes.Buffer
	logs := testAdminLogsConfig("logs/service.log")
	logs.Rotation.MaxSizeMB = 1

	result, err := newLogger(newLoggerOptions{
		enableDevLog: true,
		stdout:       &stdout,
		baseDir:      baseDir,
		logs:         logs,
	})
	if err != nil {
		t.Fatalf("newLogger() error = %v", err)
	}
	result.logger.Info(strings.Repeat("x", 1024*1024))
	result.logger.Info("rotated")
	if err := result.close(); err != nil {
		t.Fatalf("closeFn() error = %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(baseDir, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	var foundArchive bool
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".log.gz") {
			foundArchive = true
		}
	}
	if !foundArchive {
		t.Fatalf("entries = %#v, want compressed rotated archive", entries)
	}
}

func TestLoggerConversionRejectsOverflow(t *testing.T) {
	if _, err := megabytesToBytes(int(^uint(0) >> 1)); err == nil {
		t.Fatal("megabytesToBytes() error = nil, want overflow error")
	}
	if _, err := daysToDuration(int(^uint(0) >> 1)); err == nil {
		t.Fatal("daysToDuration() error = nil, want overflow error")
	}
}
