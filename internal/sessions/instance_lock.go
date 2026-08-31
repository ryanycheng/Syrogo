package sessions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var ErrInstanceLocked = errors.New("session data directory is already in use by another core instance")

type InstanceLock struct {
	file *os.File
}

func AcquireInstanceLock(dir string) (*InstanceLock, error) {
	if dir == "" {
		return nil, errors.New("session data dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session data dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure session data dir: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, "core.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open session instance lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure session instance lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrInstanceLocked, dir)
		}
		return nil, fmt.Errorf("lock session data dir: %w", err)
	}
	return &InstanceLock{file: file}, nil
}

func (l *InstanceLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		_ = file.Close()
		return fmt.Errorf("unlock session data dir: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close session instance lock: %w", err)
	}
	return nil
}
