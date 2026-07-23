package gateway

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/logging"
)

const (
	defaultAdminLogLines        = 200
	hardAdminLogLines           = 1000
	adminLogCursorVersion       = 3
	maxAdminLogFilterValue      = 256
	hardAdminLogDecompressedMax = 512 * 1024 * 1024
)

var errAdminLogCursorStale = errors.New("log cursor is stale; refresh the log query")

type adminLogQuery struct {
	Since      time.Time
	Until      time.Time
	Limit      int
	MaxBytes   int
	Cursor     string
	Text       []string
	Levels     []string
	Statuses   []string
	Clients    []string
	Inbounds   []string
	Outbounds  []string
	ErrorKinds []string
}

type adminLogItem struct {
	Time      string            `json:"time,omitempty"`
	Level     string            `json:"level,omitempty"`
	Message   string            `json:"message,omitempty"`
	Status    *int              `json:"status,omitempty"`
	Client    string            `json:"client,omitempty"`
	Inbound   string            `json:"inbound,omitempty"`
	Outbound  []string          `json:"outbound,omitempty"`
	ErrorKind string            `json:"error_kind,omitempty"`
	Duration  string            `json:"duration,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	Content   string            `json:"content"`
	Parsed    bool              `json:"parsed"`
}

type adminLogPage struct {
	Source           string
	Content          string
	Items            []adminLogItem
	Since            string
	Until            string
	Limit            int
	LineCount        int
	ScannedLineCount int
	ScannedFileCount int
	IncludesArchives bool
	BytesRead        int
	HasMore          bool
	NextCursor       string
	ScanTruncated    bool
}

type adminLogSnapshotFile struct {
	Name       string `json:"name"`
	Compressed bool   `json:"compressed,omitempty"`
	Device     uint64 `json:"dev,omitempty"`
	Inode      uint64 `json:"ino,omitempty"`
	Size       int64  `json:"size"`
	ModTime    int64  `json:"mtime"`
}

type adminLogCursor struct {
	Version    int                    `json:"v"`
	Files      []adminLogSnapshotFile `json:"files"`
	FileIndex  int                    `json:"file_index"`
	NextEnd    int64                  `json:"next_end"`
	Since      string                 `json:"since"`
	Until      string                 `json:"until"`
	Limit      int                    `json:"limit"`
	MaxBytes   int                    `json:"max_bytes"`
	FilterHash string                 `json:"filter_hash"`
}

type adminLogLine struct {
	Start         int64
	Item          adminLogItem
	EffectiveTime *time.Time
}

func hasAdminLogPageQuery(r *http.Request) bool {
	values := r.URL.Query()
	for _, key := range []string{"since", "until", "limit", "cursor", "q", "level", "status", "client", "inbound", "outbound", "error_kind"} {
		if values.Has(key) {
			return true
		}
	}
	return false
}

func parseAdminLogQuery(values map[string][]string, maxBytes int, now time.Time) (adminLogQuery, error) {
	query := adminLogQuery{Limit: defaultAdminLogLines, MaxBytes: maxBytes}
	if raw := strings.TrimSpace(firstQueryValue(values, "limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > hardAdminLogLines {
			return adminLogQuery{}, fmt.Errorf("limit must be between 1 and %d", hardAdminLogLines)
		}
		query.Limit = limit
	} else if raw := strings.TrimSpace(firstQueryValue(values, "lines")); raw != "" {
		lines, err := strconv.Atoi(raw)
		if err != nil || lines < 1 {
			return adminLogQuery{}, fmt.Errorf("lines must be a positive integer")
		}
		query.Limit = min(lines, hardAdminLogLines)
	}
	var err error
	if raw := strings.TrimSpace(firstQueryValue(values, "since")); raw != "" {
		query.Since, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return adminLogQuery{}, fmt.Errorf("since must be RFC3339")
		}
	}
	if raw := strings.TrimSpace(firstQueryValue(values, "until")); raw != "" {
		query.Until, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return adminLogQuery{}, fmt.Errorf("until must be RFC3339")
		}
	} else {
		query.Until = now
	}
	if !query.Since.IsZero() && query.Since.After(query.Until) {
		return adminLogQuery{}, fmt.Errorf("since must not be after until")
	}
	query.Cursor = strings.TrimSpace(firstQueryValue(values, "cursor"))
	query.Text, err = normalizeAdminLogValues(values["q"], false)
	if err != nil {
		return adminLogQuery{}, err
	}
	query.Levels, err = normalizeAdminLogValues(values["level"], true)
	if err != nil {
		return adminLogQuery{}, err
	}
	query.Statuses, err = normalizeAdminLogStatuses(values["status"])
	if err != nil {
		return adminLogQuery{}, err
	}
	for source, target := range map[string]*[]string{
		"client": &query.Clients, "inbound": &query.Inbounds, "outbound": &query.Outbounds, "error_kind": &query.ErrorKinds,
	} {
		*target, err = normalizeAdminLogValues(values[source], false)
		if err != nil {
			return adminLogQuery{}, err
		}
	}
	return query, nil
}

func normalizeAdminLogValues(values []string, upper bool) ([]string, error) {
	var normalized []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if len(part) > maxAdminLogFilterValue {
				return nil, fmt.Errorf("log filter value is too long")
			}
			if upper {
				part = strings.ToUpper(part)
			} else {
				part = strings.ToLower(part)
			}
			normalized = append(normalized, part)
		}
	}
	sort.Strings(normalized)
	return slicesCompact(normalized), nil
}

func normalizeAdminLogStatuses(values []string) ([]string, error) {
	statuses, err := normalizeAdminLogValues(values, false)
	if err != nil {
		return nil, err
	}
	for _, value := range statuses {
		if len(value) == 3 && value[1:] == "xx" && value[0] >= '1' && value[0] <= '5' {
			continue
		}
		status, err := strconv.Atoi(value)
		if err != nil || status < 100 || status > 599 {
			return nil, fmt.Errorf("status must be an HTTP status or status family such as 5xx")
		}
	}
	return statuses, nil
}

func slicesCompact(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func firstQueryValue(values map[string][]string, key string) string {
	if items := values[key]; len(items) > 0 {
		return items[0]
	}
	return ""
}

func adminLogPageFromRecent(snapshot logging.RecentSnapshot, query adminLogQuery) (adminLogPage, bool) {
	lines := make([]adminLogLine, 0, len(snapshot.Lines))
	for _, recent := range snapshot.Lines {
		content := strings.TrimSuffix(string(recent.Content), "\r")
		visible := redactLogContent(content)
		item := parseAdminLogItem(visible)
		effectiveTime := recent.Time
		if item.Time != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, item.Time); err == nil {
				effectiveTime = parsed
			}
		}
		value := effectiveTime
		lines = append(lines, adminLogLine{Item: item, EffectiveTime: &value})
	}
	matched := filterAdminLogLines(lines, query)
	if len(matched) > query.Limit {
		return adminLogPage{}, false
	}
	page := adminLogPage{
		Source:           "memory",
		Since:            formatAdminLogTime(query.Since),
		Until:            formatAdminLogTime(query.Until),
		Limit:            query.Limit,
		ScannedLineCount: len(lines),
		BytesRead:        int(snapshot.Bytes),
	}
	for index := len(matched) - 1; index >= 0; index-- {
		page.Items = append(page.Items, matched[index].Item)
	}
	page.LineCount = len(page.Items)
	contents := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		contents = append(contents, item.Content)
	}
	page.Content = strings.Join(contents, "\n")
	return page, true
}

func readAdminLogPage(logs config.AdminLogsConfig, query adminLogQuery) (adminLogPage, error) {
	path := logs.Path
	if path == "" {
		path = defaultAdminLogPath
	}
	filterHash := adminLogFilterHash(query)
	cursor := adminLogCursor{
		Version: adminLogCursorVersion, Since: formatAdminLogTime(query.Since), Until: formatAdminLogTime(query.Until),
		Limit: query.Limit, MaxBytes: query.MaxBytes, FilterHash: filterHash,
	}
	var resolved []string
	var err error
	if query.Cursor == "" {
		cursor.Files, resolved, err = snapshotAdminLogFiles(path, logs.Rotation)
		if err != nil {
			return adminLogPage{}, err
		}
		if len(cursor.Files) > 0 {
			cursor.NextEnd, err = adminLogSnapshotContentSize(resolved[0], cursor.Files[0], logs.Rotation)
		}
	} else {
		cursor, err = decodeAdminLogCursor(query.Cursor)
		if err != nil {
			return adminLogPage{}, err
		}
		if cursor.Since != formatAdminLogTime(query.Since) || cursor.Until != formatAdminLogTime(query.Until) || cursor.Limit != query.Limit || cursor.MaxBytes != query.MaxBytes || cursor.FilterHash != filterHash {
			return adminLogPage{}, fmt.Errorf("cursor does not match the log query")
		}
		resolved, err = resolveAdminLogSnapshot(path, cursor.Files)
	}
	if err != nil {
		return adminLogPage{}, err
	}
	if cursor.FileIndex < 0 || cursor.FileIndex > len(cursor.Files) {
		return adminLogPage{}, fmt.Errorf("invalid log cursor file index")
	}

	page := adminLogPage{
		Source: "file", Since: cursor.Since, Until: cursor.Until, Limit: query.Limit,
		IncludesArchives: len(cursor.Files) > 1,
	}
	remaining := int64(query.MaxBytes)
	for cursor.FileIndex < len(cursor.Files) && remaining > 0 && page.LineCount < query.Limit {
		entry := cursor.Files[cursor.FileIndex]
		if cursor.NextEnd < 0 {
			return adminLogPage{}, fmt.Errorf("invalid log cursor offset")
		}
		if cursor.NextEnd == 0 {
			cursor.FileIndex++
			if cursor.FileIndex < len(cursor.Files) {
				cursor.NextEnd, err = adminLogSnapshotContentSize(resolved[cursor.FileIndex], cursor.Files[cursor.FileIndex], logs.Rotation)
				if err != nil {
					return adminLogPage{}, err
				}
			}
			continue
		}
		page.ScannedFileCount++
		oldEnd := cursor.NextEnd
		start := max(int64(0), oldEnd-remaining)
		buffer, err := readAdminLogSnapshotRange(resolved[cursor.FileIndex], entry, logs.Rotation, start, oldEnd)
		if err != nil {
			return adminLogPage{}, err
		}
		lines, alignedStart := splitAdminLogLines(buffer, start, oldEnd, start > 0)
		page.ScannedLineCount += len(lines)

		nextEnd := alignedStart
		matched := make([]adminLogLine, 0, min(len(lines), query.Limit-page.LineCount))
		for index := len(lines) - 1; index >= 0; index-- {
			line := lines[index]
			if !adminLogLineMatches(line, query) {
				continue
			}
			matched = append(matched, line)
			if page.LineCount+len(matched) == query.Limit {
				nextEnd = line.Start
				break
			}
		}
		scanned := oldEnd - start
		consumed := oldEnd - nextEnd
		if consumed <= 0 {
			// A line larger than the entire budget cannot be returned, but must not deadlock pagination.
			nextEnd = start
		}
		page.BytesRead += int(scanned)
		remaining -= scanned
		cursor.NextEnd = nextEnd
		for index := len(matched) - 1; index >= 0; index-- {
			page.Items = append(page.Items, matched[index].Item)
			page.LineCount++
		}
		if cursor.NextEnd == 0 {
			cursor.FileIndex++
			if cursor.FileIndex < len(cursor.Files) {
				cursor.NextEnd, err = adminLogSnapshotContentSize(resolved[cursor.FileIndex], cursor.Files[cursor.FileIndex], logs.Rotation)
				if err != nil {
					return adminLogPage{}, err
				}
			}
		}
	}

	contents := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		contents = append(contents, item.Content)
	}
	page.Content = strings.Join(contents, "\n")
	page.HasMore = cursor.FileIndex < len(cursor.Files)
	page.ScanTruncated = page.HasMore && remaining <= 0
	if page.HasMore {
		page.NextCursor, err = encodeAdminLogCursor(cursor)
		if err != nil {
			return adminLogPage{}, err
		}
	}
	return page, nil
}

func snapshotAdminLogFiles(path string, rotation config.AdminLogsRotationConfig) ([]adminLogSnapshotFile, []string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("log path is not a regular file")
	}
	current, err := plainAdminLogSnapshot(filepath.Base(path), info)
	if err != nil {
		return nil, nil, err
	}
	files := []adminLogSnapshotFile{current}
	paths := []string{path}
	archives, err := logging.DiscoverArchives(path)
	if err != nil {
		return nil, nil, err
	}
	for _, archive := range archives {
		entry := adminLogSnapshotFile{Name: archive.Name, Compressed: archive.Compressed, Size: archive.Size, ModTime: archive.ModTime.UnixNano()}
		if !archive.Compressed {
			info, err := os.Stat(archive.Path)
			if err != nil {
				return nil, nil, err
			}
			entry, err = plainAdminLogSnapshot(archive.Name, info)
			if err != nil {
				return nil, nil, err
			}
		}
		files = append(files, entry)
		paths = append(paths, archive.Path)
	}
	return files, paths, nil
}

func plainAdminLogSnapshot(name string, info os.FileInfo) (adminLogSnapshotFile, error) {
	device, inode, err := adminLogFileIdentity(info)
	if err != nil {
		return adminLogSnapshotFile{}, err
	}
	return adminLogSnapshotFile{Name: name, Device: device, Inode: inode, Size: info.Size(), ModTime: info.ModTime().UnixNano()}, nil
}

func resolveAdminLogSnapshot(path string, files []adminLogSnapshotFile) ([]string, error) {
	if len(files) == 0 || files[0].Compressed || files[0].Name != filepath.Base(path) {
		return nil, fmt.Errorf("invalid log cursor snapshot")
	}
	archives, err := logging.DiscoverArchives(path)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		path       string
		name       string
		compressed bool
		info       os.FileInfo
	}
	candidates := make([]candidate, 0, len(archives)+1)
	if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
		candidates = append(candidates, candidate{path: path, name: filepath.Base(path), info: info})
	}
	for _, archive := range archives {
		info, statErr := os.Stat(archive.Path)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		candidates = append(candidates, candidate{path: archive.Path, name: archive.Name, compressed: archive.Compressed, info: info})
	}
	resolved := make([]string, len(files))
	used := make(map[int]bool, len(files))
	for index, entry := range files {
		if filepath.Base(entry.Name) != entry.Name || entry.Name == "." || entry.Name == ".." {
			return nil, fmt.Errorf("invalid log cursor snapshot")
		}
		found := -1
		for candidateIndex, item := range candidates {
			if used[candidateIndex] || item.compressed != entry.Compressed {
				continue
			}
			if entry.Compressed {
				if item.name != entry.Name || item.info.Size() != entry.Size || item.info.ModTime().UnixNano() != entry.ModTime {
					continue
				}
			} else {
				device, inode, identityErr := adminLogFileIdentity(item.info)
				if identityErr != nil || device != entry.Device || inode != entry.Inode {
					continue
				}
				if index != 0 && (item.name != entry.Name || item.info.Size() != entry.Size || item.info.ModTime().UnixNano() != entry.ModTime) {
					continue
				}
				if index == 0 && item.info.Size() < entry.Size {
					continue
				}
			}
			found = candidateIndex
			break
		}
		if found < 0 {
			return nil, errAdminLogCursorStale
		}
		used[found] = true
		resolved[index] = candidates[found].path
	}
	return resolved, nil
}

func adminLogSnapshotContentSize(path string, entry adminLogSnapshotFile, rotation config.AdminLogsRotationConfig) (int64, error) {
	if !entry.Compressed {
		return entry.Size, nil
	}
	content, err := readAdminLogSnapshotContent(path, entry, rotation)
	return int64(len(content)), err
}

func readAdminLogSnapshotRange(path string, entry adminLogSnapshotFile, rotation config.AdminLogsRotationConfig, start, end int64) ([]byte, error) {
	if start < 0 || end < start {
		return nil, fmt.Errorf("invalid log cursor offset")
	}
	if entry.Compressed {
		content, err := readAdminLogSnapshotContent(path, entry, rotation)
		if err != nil {
			return nil, err
		}
		if end > int64(len(content)) {
			return nil, errAdminLogCursorStale
		}
		return content[start:end], nil
	}
	if end > entry.Size {
		return nil, errAdminLogCursorStale
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content := make([]byte, end-start)
	if len(content) > 0 {
		if _, err := file.ReadAt(content, start); err != nil && err != io.EOF {
			return nil, err
		}
	}
	return content, nil
}

func readAdminLogSnapshotContent(path string, entry adminLogSnapshotFile, rotation config.AdminLogsRotationConfig) ([]byte, error) {
	if !entry.Compressed {
		return readAdminLogSnapshotRange(path, entry, rotation, 0, entry.Size)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("read gzip log archive %s: %w", entry.Name, err)
	}
	defer func() { _ = reader.Close() }()
	maxSize := int64(rotation.MaxSizeMB) * 1024 * 1024
	if maxSize <= 0 || maxSize > hardAdminLogDecompressedMax {
		maxSize = hardAdminLogDecompressedMax
	}
	limited := io.LimitReader(reader, maxSize+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read gzip log archive %s: %w", entry.Name, err)
	}
	if int64(len(content)) > maxSize {
		return nil, fmt.Errorf("read gzip log archive %s: decompressed size exceeds limit", entry.Name)
	}
	if err := reader.Close(); err != nil {
		return nil, fmt.Errorf("read gzip log archive %s: %w", entry.Name, err)
	}
	return content, nil
}

func adminLogFileIdentity(info os.FileInfo) (uint64, uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("log file identity is unavailable")
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}

func splitAdminLogLines(buffer []byte, start, end int64, discardPartial bool) ([]adminLogLine, int64) {
	text := string(buffer)
	alignedStart := start
	if discardPartial {
		if index := strings.IndexByte(text, '\n'); index >= 0 {
			text = text[index+1:]
			alignedStart += int64(index + 1)
		} else {
			return nil, end
		}
	}
	parts := strings.Split(text, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	lines := make([]adminLogLine, 0, len(parts))
	offset := alignedStart
	var effectiveTime *time.Time
	for _, rawPart := range parts {
		part := strings.TrimSuffix(rawPart, "\r")
		visible := redactLogContent(part)
		item := parseAdminLogItem(visible)
		if item.Time != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, item.Time); err == nil {
				value := parsed
				effectiveTime = &value
			}
		}
		lines = append(lines, adminLogLine{Start: offset, Item: item, EffectiveTime: effectiveTime})
		offset += int64(len(rawPart) + 1)
	}
	return lines, alignedStart
}

func parseAdminLogItem(content string) adminLogItem {
	item := adminLogItem{Content: content, Fields: make(map[string]string)}
	fields, ok := parseSlogTextFields(content)
	if !ok {
		return item
	}
	item.Parsed = true
	item.Level = strings.ToUpper(lastField(fields, "level"))
	item.Message = lastField(fields, "msg")
	if value := lastField(fields, "time"); value != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			item.Time = formatAdminLogTime(parsed)
		}
	}
	if value := firstNonEmptyField(fields, "client_name", "client"); value != "" {
		item.Client = value
	}
	item.Inbound = firstNonEmptyField(fields, "inbound", "inbound_name")
	item.ErrorKind = lastField(fields, "error_kind")
	item.Duration = firstNonEmptyField(fields, "duration", "duration_ms")
	if value := lastField(fields, "status"); value != "" {
		if status, err := strconv.Atoi(value); err == nil {
			item.Status = &status
		}
	}
	for _, key := range []string{"outbound", "outbound_name", "resolved_to"} {
		for _, value := range fields[key] {
			for _, outbound := range strings.Split(value, ",") {
				if outbound = strings.TrimSpace(outbound); outbound != "" {
					item.Outbound = append(item.Outbound, outbound)
				}
			}
		}
	}
	for key, values := range fields {
		if key == "time" || key == "level" || key == "msg" {
			continue
		}
		item.Fields[key] = strings.Join(values, ", ")
	}
	return item
}

func parseSlogTextFields(line string) (map[string][]string, bool) {
	fields := make(map[string][]string)
	for index := 0; index < len(line); {
		for index < len(line) && line[index] == ' ' {
			index++
		}
		if index == len(line) {
			break
		}
		key, next, ok := parseSlogToken(line, index, '=')
		if !ok || next >= len(line) || line[next] != '=' {
			return fields, false
		}
		value, end, ok := parseSlogToken(line, next+1, ' ')
		if !ok {
			return fields, false
		}
		fields[key] = append(fields[key], value)
		index = end
	}
	return fields, len(fields) > 0
}

func parseSlogToken(line string, start int, delimiter byte) (string, int, bool) {
	if start >= len(line) {
		return "", start, true
	}
	if line[start] == '"' {
		for index := start + 1; index < len(line); index++ {
			if line[index] == '\\' {
				index++
				continue
			}
			if line[index] == '"' {
				value, err := strconv.Unquote(line[start : index+1])
				return value, index + 1, err == nil
			}
		}
		return "", len(line), false
	}
	end := start
	for end < len(line) && line[end] != delimiter {
		end++
	}
	return line[start:end], end, true
}

func lastField(fields map[string][]string, key string) string {
	values := fields[key]
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func firstNonEmptyField(fields map[string][]string, keys ...string) string {
	for _, key := range keys {
		if value := lastField(fields, key); value != "" {
			return value
		}
	}
	return ""
}

func adminLogLineMatches(line adminLogLine, query adminLogQuery) bool {
	if line.EffectiveTime != nil {
		if !query.Since.IsZero() && line.EffectiveTime.Before(query.Since) {
			return false
		}
		if !query.Until.IsZero() && line.EffectiveTime.After(query.Until) {
			return false
		}
	}
	return matchAdminLogItem(line.Item, query)
}

func filterAdminLogLines(lines []adminLogLine, query adminLogQuery) []adminLogLine {
	filtered := make([]adminLogLine, 0, len(lines))
	for _, line := range lines {
		if adminLogLineMatches(line, query) {
			filtered = append(filtered, line)
		}
	}
	return filtered
}

func matchAdminLogItem(item adminLogItem, query adminLogQuery) bool {
	if !matchStringValues(item.Level, query.Levels, true) || !matchStatus(item.Status, query.Statuses) ||
		!matchStringValues(item.Client, query.Clients, false) || !matchStringValues(item.Inbound, query.Inbounds, false) ||
		!matchStringValues(item.ErrorKind, query.ErrorKinds, false) || !matchAnyStringValues(item.Outbound, query.Outbounds) {
		return false
	}
	searchable := strings.ToLower(item.Content + " " + item.Message)
	for _, value := range item.Fields {
		searchable += " " + strings.ToLower(value)
	}
	for _, term := range query.Text {
		if !strings.Contains(searchable, term) {
			return false
		}
	}
	return true
}

func matchStringValues(value string, filters []string, upper bool) bool {
	if len(filters) == 0 {
		return true
	}
	if upper {
		value = strings.ToUpper(value)
	} else {
		value = strings.ToLower(value)
	}
	for _, filter := range filters {
		if value == filter {
			return true
		}
	}
	return false
}

func matchAnyStringValues(values, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, value := range values {
		if matchStringValues(value, filters, false) {
			return true
		}
	}
	return false
}

func matchStatus(status *int, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	if status == nil {
		return false
	}
	for _, filter := range filters {
		if len(filter) == 3 && filter[1:] == "xx" && *status/100 == int(filter[0]-'0') {
			return true
		}
		if value, _ := strconv.Atoi(filter); value == *status {
			return true
		}
	}
	return false
}

func adminLogFilterHash(query adminLogQuery) string {
	payload, _ := json.Marshal(struct {
		Text, Levels, Statuses, Clients, Inbounds, Outbounds, ErrorKinds []string
	}{query.Text, query.Levels, query.Statuses, query.Clients, query.Inbounds, query.Outbounds, query.ErrorKinds})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func formatAdminLogTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func encodeAdminLogCursor(cursor adminLogCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeAdminLogCursor(value string) (adminLogCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return adminLogCursor{}, fmt.Errorf("invalid log cursor")
	}
	var cursor adminLogCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Version != adminLogCursorVersion {
		return adminLogCursor{}, fmt.Errorf("invalid log cursor")
	}
	return cursor, nil
}
