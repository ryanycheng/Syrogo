package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type sessionHookCLIOptions struct {
	Event  string
	Stdin  io.Reader
	Stderr io.Writer
}

type sessionHookForwardRequest struct {
	SessionID   string         `json:"session_id"`
	InboundName string         `json:"inbound_name"`
	EventName   string         `json:"event_name"`
	Payload     map[string]any `json:"payload"`
	ReceivedAt  time.Time      `json:"received_at"`
}

func runSession(args []string) int {
	if len(args) == 0 || args[0] != "hook-event" {
		_, _ = fmt.Fprintln(os.Stderr, "usage: syrogo session hook-event --event <EventName>")
		return 2
	}
	opts := sessionHookCLIOptions{Stdin: os.Stdin, Stderr: os.Stderr}
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--event", "-event":
			if index+1 >= len(args) {
				_, _ = fmt.Fprintln(os.Stderr, "--event requires a value")
				return 2
			}
			opts.Event = args[index+1]
			index++
		default:
			if value, ok := strings.CutPrefix(args[index], "--event="); ok {
				opts.Event = value
				continue
			}
			_, _ = fmt.Fprintf(os.Stderr, "unknown session option %q\n", args[index])
			return 2
		}
	}
	return runSessionHookEvent(opts)
}

func runSessionHookEvent(opts sessionHookCLIOptions) int {
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Event == "" {
		_, _ = fmt.Fprintln(opts.Stderr, "syrogo session hook-event: missing --event")
		return 0
	}
	sessionID := os.Getenv("SYROGO_SESSION_ID")
	baseURL := os.Getenv("SYROGO_BASE_URL")
	token := os.Getenv("SYROGO_SESSION_AUTH_TOKEN")
	inboundName := os.Getenv("SYROGO_SESSION_INBOUND_NAME")
	if sessionID == "" || baseURL == "" || token == "" || inboundName == "" {
		_, _ = fmt.Fprintln(opts.Stderr, "syrogo session hook-event: missing session environment")
		return 0
	}
	payload := map[string]any{}
	data, err := io.ReadAll(opts.Stdin)
	if err == nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &payload); err != nil {
			payload = map[string]any{"parse_error": err.Error()}
		}
	}
	req := sessionHookForwardRequest{SessionID: sessionID, InboundName: inboundName, EventName: opts.Event, Payload: payload, ReceivedAt: time.Now()}
	if err := postSessionJSON(baseURL, "/session/hook-event", token, req); err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "syrogo session hook-event: %v\n", err)
	}
	return 0
}

type sessionHTTPError struct {
	Path       string
	StatusCode int
	Status     string
}

func (e *sessionHTTPError) Error() string {
	return fmt.Sprintf("%s returned %s", e.Path, e.Status)
}

func postSessionJSON(baseURL string, path string, token string, body any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return postSessionJSONContext(ctx, baseURL, path, token, body)
}

func postSessionJSONContext(ctx context.Context, baseURL string, path string, token string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &sessionHTTPError{Path: path, StatusCode: resp.StatusCode, Status: resp.Status}
	}
	return nil
}
