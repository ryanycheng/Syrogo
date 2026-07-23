package logging

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options configures a RotatingWriter. Retention limits apply only to archives.
type Options struct {
	Path         string
	MaxSizeBytes int64
	MaxFiles     int
	MaxAge       time.Duration
	MaxTotalSize int64
	Compress     bool
	ErrorWriter  io.Writer
	Now          func() time.Time
}

// Archive describes a valid archive managed by RotatingWriter.
type Archive struct {
	Path       string
	Name       string
	Compressed bool
	Identity   string
	ModTime    time.Time
	Size       int64
	stamp      time.Time
	nanos      uint64
	sequence   uint64
}

// RotatingWriter rotates a file before a write that would cross a size limit,
// or when the local calendar date changes.
type RotatingWriter struct {
	mu       sync.Mutex
	opts     Options
	file     *os.File
	size     int64
	openDate string
	sequence uint64
	wake     chan struct{}
	stop     chan struct{}
	wg       sync.WaitGroup
	closed   bool
}

// NewRotatingWriter opens opts.Path and starts its archive worker.
func NewRotatingWriter(opts Options) (*RotatingWriter, error) {
	if opts.Path == "" {
		return nil, errors.New("logging: path is required")
	}
	if opts.MaxSizeBytes <= 0 {
		return nil, errors.New("logging: max size must be greater than zero")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o755); err != nil {
		return nil, fmt.Errorf("logging: create directory: %w", err)
	}
	file, err := os.OpenFile(opts.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("logging: open current file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("logging: stat current file: %w", err)
	}
	now := opts.Now()
	w := &RotatingWriter{
		opts:     opts,
		file:     file,
		size:     info.Size(),
		openDate: localDate(now),
		wake:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
	}
	w.wg.Add(1)
	go w.worker()
	w.signalWorker()
	return w, nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	now := w.opts.Now()
	if (w.size > 0 && w.size+int64(len(p)) > w.opts.MaxSizeBytes) || localDate(now) != w.openDate {
		if _, err := w.rotate(now); err != nil {
			return 0, err
		}
		w.signalWorker()
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) rotate(now time.Time) (string, error) {
	if err := w.file.Close(); err != nil {
		return "", fmt.Errorf("logging: close for rotation: %w", err)
	}
	archive, err := w.nextArchiveName(now)
	if err != nil {
		w.reopenCurrent(now)
		return "", err
	}
	if err := os.Rename(w.opts.Path, archive); err != nil {
		w.reopenCurrent(now)
		return "", fmt.Errorf("logging: archive current file: %w", err)
	}
	if err := w.reopenCurrent(now); err != nil {
		return "", err
	}
	return archive, nil
}

func (w *RotatingWriter) reopenCurrent(now time.Time) error {
	file, err := os.OpenFile(w.opts.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		w.file = nil
		return fmt.Errorf("logging: reopen current file: %w", err)
	}
	w.file = file
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		w.file = nil
		return fmt.Errorf("logging: stat reopened file: %w", err)
	}
	w.size = info.Size()
	w.openDate = localDate(now)
	return nil
}

func (w *RotatingWriter) nextArchiveName(now time.Time) (string, error) {
	dir, prefix := archiveParts(w.opts.Path)
	stamp := now.Format("20060102-150405")
	nanos := fmt.Sprintf("%09d", now.Nanosecond())
	for {
		w.sequence++
		name := fmt.Sprintf("%s.%s.%s.%d.log", prefix, stamp, nanos, w.sequence)
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return path, nil
		} else if err != nil {
			return "", fmt.Errorf("logging: inspect archive path: %w", err)
		}
	}
}

// Close closes the current file and waits for pending compression and cleanup.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	var err error
	if w.file != nil {
		err = w.file.Close()
	}
	close(w.stop)
	w.mu.Unlock()
	w.wg.Wait()
	return err
}

func (w *RotatingWriter) signalWorker() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *RotatingWriter) worker() {
	defer w.wg.Done()
	for {
		select {
		case <-w.wake:
			w.processArchives()
		case <-w.stop:
			w.processArchives()
			return
		}
	}
}

func (w *RotatingWriter) processArchives() {
	if w.opts.Compress {
		archives, err := DiscoverArchives(w.opts.Path)
		if err != nil {
			w.reportError(err)
		} else {
			for _, archive := range archives {
				if archive.Compressed {
					continue
				}
				if err := gzipArchive(archive.Path); err != nil {
					w.reportError(err)
				}
			}
		}
	}
	if err := cleanup(w.opts); err != nil {
		w.reportError(err)
	}
}

func (w *RotatingWriter) reportError(err error) {
	if w.opts.ErrorWriter == nil {
		return
	}
	if target, ok := w.opts.ErrorWriter.(*RotatingWriter); ok && target == w {
		return
	}
	_, _ = fmt.Fprintf(w.opts.ErrorWriter, "logging rotation: %v\n", err)
}

func gzipArchive(path string) error {
	source, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open archive for gzip: %w", err)
	}
	defer source.Close()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".log-gzip-*.tmp")
	if err != nil {
		return fmt.Errorf("create gzip temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	zw := gzip.NewWriter(tmp)
	if _, err := io.Copy(zw, source); err != nil {
		_ = zw.Close()
		return fmt.Errorf("compress archive: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finish archive gzip: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close archive gzip: %w", err)
	}
	gzPath := path + ".gz"
	if err := os.Rename(tmpPath, gzPath); err != nil {
		return fmt.Errorf("publish archive gzip: %w", err)
	}
	keep = true
	if err := os.Remove(path); err != nil {
		_ = os.Remove(gzPath)
		return fmt.Errorf("remove uncompressed archive: %w", err)
	}
	return nil
}

func cleanup(opts Options) error {
	archives, err := DiscoverArchives(opts.Path)
	if err != nil {
		return err
	}
	now := opts.Now()
	kept := make([]Archive, 0, len(archives))
	var errs []error
	for _, archive := range archives {
		if opts.MaxAge > 0 && now.Sub(archive.ModTime) > opts.MaxAge {
			if err := os.Remove(archive.Path); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		kept = append(kept, archive)
	}
	if opts.MaxFiles > 0 && len(kept) > opts.MaxFiles {
		for _, archive := range kept[opts.MaxFiles:] {
			if err := os.Remove(archive.Path); err != nil {
				errs = append(errs, err)
			}
		}
		kept = kept[:opts.MaxFiles]
	}
	if opts.MaxTotalSize > 0 {
		var total int64
		if info, err := os.Stat(opts.Path); err == nil && info.Mode().IsRegular() {
			total = info.Size()
		} else if err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
		for _, archive := range kept {
			total += archive.Size
		}
		for i := len(kept) - 1; i >= 0 && total > opts.MaxTotalSize; i-- {
			if err := os.Remove(kept[i].Path); err != nil {
				errs = append(errs, err)
				continue
			}
			total -= kept[i].Size
		}
	}
	return errors.Join(errs...)
}

// DiscoverArchives returns strictly named, regular archives from newest to oldest.
func DiscoverArchives(currentPath string) ([]Archive, error) {
	dir, prefix := archiveParts(currentPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("logging: read archive directory: %w", err)
	}
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `\.(\d{8}-\d{6})\.(\d{9})\.(\d+)\.log(\.gz)?$`)
	archives := make([]Archive, 0)
	for _, entry := range entries {
		match := pattern.FindStringSubmatch(entry.Name())
		if match == nil || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		stamp, err := time.ParseInLocation("20060102-150405", match[1], time.Local)
		if err != nil {
			continue
		}
		nanos, err := strconv.ParseUint(match[2], 10, 32)
		if err != nil || nanos >= uint64(time.Second) {
			continue
		}
		sequence, err := strconv.ParseUint(match[3], 10, 64)
		if err != nil || sequence == 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		archives = append(archives, Archive{
			Path:       path,
			Name:       entry.Name(),
			Compressed: strings.HasSuffix(entry.Name(), ".gz"),
			Identity:   fmt.Sprintf("%s:%d:%d", entry.Name(), info.ModTime().UnixNano(), info.Size()),
			ModTime:    info.ModTime(),
			Size:       info.Size(),
			stamp:      stamp,
			nanos:      nanos,
			sequence:   sequence,
		})
	}
	sort.Slice(archives, func(i, j int) bool {
		if !archives[i].stamp.Equal(archives[j].stamp) {
			return archives[i].stamp.After(archives[j].stamp)
		}
		if archives[i].nanos != archives[j].nanos {
			return archives[i].nanos > archives[j].nanos
		}
		if archives[i].sequence != archives[j].sequence {
			return archives[i].sequence > archives[j].sequence
		}
		return archives[i].Compressed && !archives[j].Compressed
	})
	return archives, nil
}

func archiveParts(path string) (string, string) {
	dir := filepath.Dir(path)
	name := filepath.Base(path)
	prefix := strings.TrimSuffix(name, filepath.Ext(name))
	return dir, prefix
}

func localDate(t time.Time) string { return t.Format("2006-01-02") }
