package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const devLogPath = "tmp/dev.log"

type newLoggerOptions struct {
	enableDevLog bool
	stdout       io.Writer
	baseDir      string
}

func newLogger(opts newLoggerOptions) (*slog.Logger, func() error, error) {
	stdout := opts.stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	writer := stdout
	closeFn := func() error { return nil }
	if opts.enableDevLog {
		logFile, err := openDevLogFile(opts.baseDir)
		if err != nil {
			return nil, nil, err
		}
		writer = io.MultiWriter(stdout, logFile)
		closeFn = logFile.Close
	}

	return slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{})), closeFn, nil
}

func openDevLogFile(baseDir string) (*os.File, error) {
	logPath := devLogPath
	if baseDir != "" {
		logPath = filepath.Join(baseDir, devLogPath)
	}

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}
