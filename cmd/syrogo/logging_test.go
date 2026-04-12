package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLoggerWithDevLogWritesStdoutAndFile(t *testing.T) {
	baseDir := t.TempDir()
	var stdout bytes.Buffer

	logger, closeFn, err := newLogger(newLoggerOptions{
		enableDevLog: true,
		stdout:       &stdout,
		baseDir:      baseDir,
	})
	if err != nil {
		t.Fatalf("newLogger() error = %v", err)
	}
	defer closeFn()

	logger.Info("hello dev log", slog.String("component", "test"))

	gotStdout := stdout.String()
	if !strings.Contains(gotStdout, "hello dev log") {
		t.Fatalf("stdout logs = %q, want hello dev log", gotStdout)
	}

	logFilePath := filepath.Join(baseDir, devLogPath)
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

	logger, closeFn, err := newLogger(newLoggerOptions{
		enableDevLog: false,
		stdout:       &stdout,
		baseDir:      baseDir,
	})
	if err != nil {
		t.Fatalf("newLogger() error = %v", err)
	}
	defer closeFn()

	logger.Info("stdout only")

	if !strings.Contains(stdout.String(), "stdout only") {
		t.Fatalf("stdout logs = %q, want stdout only", stdout.String())
	}

	if _, err := os.Stat(filepath.Join(baseDir, devLogPath)); !os.IsNotExist(err) {
		t.Fatalf("dev log file existence error = %v, want not exists", err)
	}
}

func TestOpenDevLogFileCreatesTmpDir(t *testing.T) {
	baseDir := t.TempDir()

	file, err := openDevLogFile(baseDir)
	if err != nil {
		t.Fatalf("openDevLogFile() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("file.Close() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, "tmp")); err != nil {
		t.Fatalf("os.Stat(tmp) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, devLogPath)); err != nil {
		t.Fatalf("os.Stat(dev.log) error = %v", err)
	}
}
