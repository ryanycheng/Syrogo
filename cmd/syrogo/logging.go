package main

import (
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
	internallogging "github.com/ryanycheng/Syrogo/internal/logging"
)

type newLoggerOptions struct {
	enableDevLog bool
	stdout       io.Writer
	baseDir      string
	logs         config.AdminLogsConfig
}

type loggerResult struct {
	logger     *slog.Logger
	close      func() error
	recentLogs *internallogging.RecentBuffer
}

func newLogger(opts newLoggerOptions) (loggerResult, error) {
	stdout := opts.stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	writer := stdout
	closeFn := func() error { return nil }
	if opts.enableDevLog {
		rotation := opts.logs.Rotation
		maxSize, err := megabytesToBytes(rotation.MaxSizeMB)
		if err != nil {
			return loggerResult{}, fmt.Errorf("admin.logs.rotation.max_size_mb: %w", err)
		}
		maxTotalSize, err := megabytesToBytes(rotation.MaxTotalSizeMB)
		if err != nil {
			return loggerResult{}, fmt.Errorf("admin.logs.rotation.max_total_size_mb: %w", err)
		}
		maxAge, err := daysToDuration(rotation.MaxAgeDays)
		if err != nil {
			return loggerResult{}, fmt.Errorf("admin.logs.rotation.max_age_days: %w", err)
		}
		rotatingWriter, err := internallogging.NewRotatingWriter(internallogging.Options{
			Path:         devLogPath(opts),
			MaxSizeBytes: maxSize,
			MaxFiles:     rotation.MaxFiles,
			MaxAge:       maxAge,
			MaxTotalSize: maxTotalSize,
			Compress:     rotation.CompressionEnabled(),
			ErrorWriter:  stdout,
		})
		if err != nil {
			return loggerResult{}, err
		}
		const recentLogBytes = int64(8 * 1024 * 1024)
		recentLogs := internallogging.NewRecentBuffer(5*time.Minute, recentLogBytes)
		writer = io.MultiWriter(stdout, rotatingWriter, recentLogs)
		closeFn = rotatingWriter.Close
		return loggerResult{
			logger:     slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{})),
			close:      closeFn,
			recentLogs: recentLogs,
		}, nil
	}

	return loggerResult{
		logger: slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{})),
		close:  closeFn,
	}, nil
}

func devLogPath(opts newLoggerOptions) string {
	path := opts.logs.Path
	if opts.baseDir != "" && !filepath.IsAbs(path) {
		return filepath.Join(opts.baseDir, path)
	}
	return path
}

func megabytesToBytes(value int) (int64, error) {
	const bytesPerMB = int64(1024 * 1024)
	if value <= 0 || uint64(value) > uint64(math.MaxInt64/bytesPerMB) {
		return 0, fmt.Errorf("value %d is out of range", value)
	}
	return int64(value) * bytesPerMB, nil
}

func daysToDuration(value int) (time.Duration, error) {
	const day = 24 * time.Hour
	if value <= 0 || uint64(value) > uint64(math.MaxInt64/int64(day)) {
		return 0, fmt.Errorf("value %d is out of range", value)
	}
	return time.Duration(value) * day, nil
}
