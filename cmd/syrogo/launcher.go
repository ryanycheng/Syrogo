package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/sessions"
)

const (
	defaultInstalledConfigPath = "/opt/syrogo/config/config.yaml"
	sessionHeartbeatInterval   = 10 * time.Second
	sessionRequestTimeout      = 2 * time.Second
)

var installedConfigPath = defaultInstalledConfigPath

type launcherOptions struct {
	ConfigPath string
	BaseURL    string
	Client     string
	Inbound    string
	Token      string
	PrintEnv   bool
	Args       []string
	Stdout     io.Writer
	Stderr     io.Writer
	Stdin      io.Reader
}

type launchPlan struct {
	Command string
	Args    []string
	Env     map[string]string
	Client  launcherClient
}

func runLauncher(args []string) int {
	opts := launcherOptions{Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin}
	if err := parseLauncherOptions(args, &opts); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := launchAgent(opts); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runActivate(args []string) int {
	opts := launcherOptions{Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin}
	if err := parseActivateOptions(args, &opts); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 2
	}
	plan, err := buildLaunchPlan(opts)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := printShellExports(opts.Stdout, plan.Env); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func parseActivateOptions(args []string, opts *launcherOptions) error {
	if len(args) == 0 {
		return errors.New("usage: syrogo activate <claude|codex> [--config path] [--client name] [--inbound name] [--base-url url] [--token token]")
	}
	agent := args[0]
	if agent != "claude" && agent != "codex" {
		return fmt.Errorf("unsupported agent %q", agent)
	}

	fs := flag.NewFlagSet("activate "+agent, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.ConfigPath, "config", defaultLauncherConfigPath(), "path to config file")
	fs.StringVar(&opts.BaseURL, "base-url", "", "Syrogo base URL")
	fs.StringVar(&opts.Client, "client", "", "client name in config")
	fs.StringVar(&opts.Inbound, "inbound", "", "inbound name in config")
	fs.StringVar(&opts.Token, "token", "", "override Syrogo client token")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) > 0 {
		return errors.New("activate does not accept agent command arguments")
	}
	if opts.BaseURL == "" {
		baseURL, err := inferLauncherBaseURL(opts.ConfigPath, opts.Inbound, opts.Client, agent)
		if err != nil {
			return err
		}
		opts.BaseURL = baseURL
	}
	opts.Args = []string{agent}
	return nil
}
func parseLauncherOptions(args []string, opts *launcherOptions) error {
	if len(args) == 0 {
		return errors.New("usage: syrogo run <claude|codex> [--config path] [--client name] [--inbound name] [--base-url url] [--token token] [--print-env] [-- <args>]")
	}
	agent := args[0]
	if agent != "claude" && agent != "codex" {
		return fmt.Errorf("unsupported agent %q", agent)
	}

	runArgs, agentArgs := splitLauncherArgs(args[1:])
	fs := flag.NewFlagSet("run "+agent, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.ConfigPath, "config", defaultLauncherConfigPath(), "path to config file")
	fs.StringVar(&opts.BaseURL, "base-url", "", "Syrogo base URL")
	fs.StringVar(&opts.Client, "client", "", "client name in config")
	fs.StringVar(&opts.Inbound, "inbound", "", "inbound name in config")
	fs.StringVar(&opts.Token, "token", "", "override Syrogo client token")
	fs.BoolVar(&opts.PrintEnv, "print-env", false, "print launcher environment instead of executing")
	if err := fs.Parse(runArgs); err != nil {
		return err
	}
	if opts.BaseURL == "" {
		baseURL, err := inferLauncherBaseURL(opts.ConfigPath, opts.Inbound, opts.Client, agent)
		if err != nil {
			return err
		}
		opts.BaseURL = baseURL
	}
	opts.Args = append([]string{agent}, agentArgs...)
	return nil
}

func splitLauncherArgs(args []string) ([]string, []string) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			return append([]string(nil), args[:index]...), append([]string(nil), args[index+1:]...)
		}
		if !isLauncherFlag(arg) {
			return append([]string(nil), args[:index]...), append([]string(nil), args[index:]...)
		}
		if requiresLauncherFlagValue(arg) && !strings.Contains(arg, "=") {
			index++
		}
	}
	return append([]string(nil), args...), nil
}

func isLauncherFlag(arg string) bool {
	if strings.Contains(arg, "=") {
		name, _, _ := strings.Cut(arg, "=")
		arg = name
	}
	switch arg {
	case "--config", "-config", "--base-url", "-base-url", "--client", "-client", "--inbound", "-inbound", "--token", "-token", "--print-env", "-print-env":
		return true
	default:
		return false
	}
}

func requiresLauncherFlagValue(arg string) bool {
	switch arg {
	case "--config", "-config", "--base-url", "-base-url", "--client", "-client", "--inbound", "-inbound", "--token", "-token":
		return true
	default:
		return false
	}
}

func defaultLauncherConfigPath() string {
	if _, err := os.Stat(installedConfigPath); err == nil {
		return installedConfigPath
	}
	localConfig := filepath.Join(".", "configs", "config.yaml")
	if _, err := os.Stat(localConfig); err == nil {
		return localConfig
	}
	return installedConfigPath
}

func inferLauncherBaseURL(configPath, inboundName, clientName, agent string) (string, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", err
	}
	if len(cfg.Listeners) == 0 {
		return launcherBaseURLFromListen(cfg.Server.Listen)
	}

	selectedInbound := inboundName
	if selectedInbound == "" {
		selected, err := selectLauncherClient(cfg, "", clientName, agent)
		if err != nil {
			return "", fmt.Errorf("infer launcher listener: %w", err)
		}
		selectedInbound = selected.InboundName
	} else {
		found := false
		for _, inbound := range cfg.Inbounds {
			if inbound.Name == selectedInbound {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("infer launcher listener: inbound %q not found", selectedInbound)
		}
	}

	matches := make([]config.ListenerSpec, 0, 1)
	for _, listener := range cfg.Listeners {
		for _, name := range listener.Inbounds {
			if name == selectedInbound {
				matches = append(matches, listener)
				break
			}
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("infer launcher listener: inbound %q is not bound to a listener", selectedInbound)
	}
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, listener := range matches {
			names = append(names, listener.Name)
		}
		return "", fmt.Errorf("infer launcher listener: inbound %q is bound to multiple listeners (%s); pass --base-url", selectedInbound, strings.Join(names, ", "))
	}
	return launcherBaseURLFromListen(matches[0].Listen)
}

func launcherBaseURLFromListen(listen string) (string, error) {
	if listen == "" {
		return "http://127.0.0.1:23234", nil
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		if strings.HasPrefix(listen, ":") {
			return "http://127.0.0.1" + listen, nil
		}
		return "", fmt.Errorf("infer launcher base url from listen %q: %w", listen, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, port)), nil
}

func launchAgent(opts launcherOptions) error {
	plan, err := buildLaunchPlan(opts)
	if err != nil {
		return err
	}
	if opts.PrintEnv {
		return printLaunchPlan(opts.Stdout, plan)
	}
	if plan.Command == "claude" {
		return launchClaudeAgent(opts, plan)
	}
	cmd := exec.Command(plan.Command, plan.Args...)
	cmd.Env = mergeEnv(os.Environ(), plan.Env)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	return cmd.Run()
}

func launchClaudeAgent(opts launcherOptions, plan launchPlan) error {
	sessionID := newSessionID()
	commandPath, err := os.Executable()
	if err != nil {
		commandPath = "syrogo"
	}
	settingsPath, err := prepareClaudeSettingsWithHooks(commandPath)
	if err != nil {
		return fmt.Errorf("prepare claude hooks: %w", err)
	}
	defer func() { _ = os.Remove(settingsPath) }()

	env := map[string]string{}
	maps.Copy(env, plan.Env)
	env["SYROGO_SESSION_ID"] = sessionID
	env["SYROGO_BASE_URL"] = opts.BaseURL
	env["SYROGO_SESSION_AUTH_TOKEN"] = plan.Client.Token
	env["SYROGO_SESSION_INBOUND_NAME"] = plan.Client.InboundName

	args := append([]string{"--settings", settingsPath}, plan.Args...)
	cmd := exec.Command(plan.Command, args...)
	cmd.Env = mergeEnv(os.Environ(), env)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	reporter := startClaudeSessionReporter(opts, plan, sessionID, cmd.Process.Pid)

	waitErr := cmd.Wait()
	reporter.Close()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	if err := postSessionJSON(opts.BaseURL, "/session/stopped", plan.Client.Token, map[string]any{"session_id": sessionID, "inbound_name": plan.Client.InboundName, "exit_code": exitCode}); err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "syrogo session stopped: %v\n", err)
	}
	return waitErr
}

type claudeSessionReporter struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func startClaudeSessionReporter(opts launcherOptions, plan launchPlan, sessionID string, pid int) *claudeSessionReporter {
	ctx, cancel := context.WithCancel(context.Background())
	reporter := &claudeSessionReporter{cancel: cancel}
	startedAt := time.Now()
	requestCtx, requestCancel := context.WithTimeout(ctx, sessionRequestTimeout)
	err := registerClaudeSession(requestCtx, opts, plan, sessionID, pid, startedAt)
	requestCancel()
	registered := err == nil
	if err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "syrogo session register: %v\n", err)
	}
	reporter.wg.Add(1)
	go func() {
		defer reporter.wg.Done()
		ticker := time.NewTicker(sessionHeartbeatInterval)
		defer ticker.Stop()
		runClaudeSessionReporter(ctx, opts, plan, sessionID, pid, startedAt, registered, ticker.C)
	}()
	return reporter
}

func (r *claudeSessionReporter) Close() {
	if r == nil {
		return
	}
	r.cancel()
	r.wg.Wait()
}

func runClaudeSessionReporter(ctx context.Context, opts launcherOptions, plan launchPlan, sessionID string, pid int, startedAt time.Time, registered bool, ticks <-chan time.Time) {
	for {
		select {
		case <-ticks:
		case <-ctx.Done():
			return
		}

		requestCtx, cancel := context.WithTimeout(ctx, sessionRequestTimeout)
		if !registered {
			err := registerClaudeSession(requestCtx, opts, plan, sessionID, pid, startedAt)
			cancel()
			if err == nil {
				registered = true
			} else if ctx.Err() == nil {
				_, _ = fmt.Fprintf(opts.Stderr, "syrogo session register: %v\n", err)
			}
			continue
		}

		err := heartbeatClaudeSession(requestCtx, opts, plan, sessionID)
		cancel()
		if err == nil || ctx.Err() != nil {
			continue
		}
		var httpErr *sessionHTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			requestCtx, cancel = context.WithTimeout(ctx, sessionRequestTimeout)
			err = registerClaudeSession(requestCtx, opts, plan, sessionID, pid, startedAt)
			cancel()
			registered = err == nil
			if err != nil && ctx.Err() == nil {
				_, _ = fmt.Fprintf(opts.Stderr, "syrogo session register: %v\n", err)
			}
			continue
		}
		_, _ = fmt.Fprintf(opts.Stderr, "syrogo session heartbeat: %v\n", err)
	}
}

func registerClaudeSession(ctx context.Context, opts launcherOptions, plan launchPlan, sessionID string, pid int, startedAt time.Time) error {
	host, _ := os.Hostname()
	cwd, _ := os.Getwd()
	request := map[string]any{
		"session_id":           sessionID,
		"client_name":          plan.Client.ClientName,
		"inbound_name":         plan.Client.InboundName,
		"tag":                  plan.Client.Tag,
		"host":                 host,
		"pid":                  pid,
		"cwd":                  cwd,
		"git_branch":           collectGitBranch(),
		"command":              append([]string{plan.Command}, plan.Args...),
		"tmux":                 collectTmuxInfo(),
		"started_at":           startedAt,
		"heartbeat_capability": sessions.HeartbeatCapabilityV1,
	}
	return postSessionJSONContext(ctx, opts.BaseURL, "/session/register", plan.Client.Token, request)
}

func heartbeatClaudeSession(ctx context.Context, opts launcherOptions, plan launchPlan, sessionID string) error {
	return postSessionJSONContext(ctx, opts.BaseURL, "/session/heartbeat", plan.Client.Token, map[string]any{
		"session_id":   sessionID,
		"inbound_name": plan.Client.InboundName,
	})
}

func newSessionID() string {
	return fmt.Sprintf("cc-%d-%d", time.Now().UnixNano(), os.Getpid())
}

func buildLaunchPlan(opts launcherOptions) (launchPlan, error) {
	if len(opts.Args) == 0 {
		return launchPlan{}, errors.New("agent command is required")
	}
	agent := opts.Args[0]
	token := opts.Token
	client := launcherClient{InboundName: opts.Inbound, ClientName: opts.Client, Token: token}
	if token == "" {
		cfg, err := config.Load(opts.ConfigPath)
		if err != nil {
			return launchPlan{}, err
		}
		selected, err := selectLauncherClient(cfg, opts.Inbound, opts.Client, agent)
		if err != nil {
			return launchPlan{}, err
		}
		client = selected
		token = selected.Token
	}
	if token == "" {
		return launchPlan{}, errors.New("client token is required")
	}
	if agent == "claude" && client.InboundName == "" {
		return launchPlan{}, errors.New("inbound is required for Claude session registration; pass --inbound")
	}

	env := map[string]string{}
	switch agent {
	case "claude":
		env["ANTHROPIC_BASE_URL"] = opts.BaseURL
		env["ANTHROPIC_AUTH_TOKEN"] = token
	case "codex":
		env["OPENAI_BASE_URL"] = opts.BaseURL
		env["OPENAI_API_KEY"] = token
	default:
		return launchPlan{}, fmt.Errorf("unsupported agent %q", agent)
	}
	return launchPlan{Command: agent, Args: append([]string(nil), opts.Args[1:]...), Env: env, Client: client}, nil
}

type launcherClient struct {
	InboundName string
	ClientName  string
	Token       string
	Tag         string
}

func selectLauncherClient(cfg config.Config, inboundName string, clientName string, agent string) (launcherClient, error) {
	preferredProtocol := ""
	switch agent {
	case "claude":
		preferredProtocol = "anthropic_messages"
	case "codex":
		preferredProtocol = "openai_responses"
	}
	var matches []launcherClient
	for _, inbound := range cfg.Inbounds {
		if inboundName != "" && inbound.Name != inboundName {
			continue
		}
		if inboundName == "" && preferredProtocol != "" && inbound.Protocol != preferredProtocol {
			continue
		}
		for _, binding := range inbound.Clients {
			resolved, ok := config.ResolveClientBinding(cfg, binding)
			if !ok {
				continue
			}
			if clientName != "" && resolved.Client.Name != clientName {
				continue
			}
			matches = append(matches, launcherClient{
				InboundName: inbound.Name,
				ClientName:  resolved.Client.Name,
				Token:       resolved.Client.Token,
				Tag:         binding.Tag,
			})
		}
	}
	if len(matches) == 0 {
		return launcherClient{}, fmt.Errorf("no client matched inbound=%q client=%q for %s", inboundName, clientName, agent)
	}
	if len(matches) > 1 {
		return launcherClient{}, fmt.Errorf("multiple clients matched for %s; pass --client or --inbound", agent)
	}
	return matches[0], nil
}

func printLaunchPlan(w io.Writer, plan launchPlan) error {
	keys := make([]string, 0, len(plan.Env))
	for key := range plan.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := fmt.Fprintf(w, "%s=%s\n", key, redactLaunchEnvValue(key, plan.Env[key])); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "command=%s", plan.Command); err != nil {
		return err
	}
	if len(plan.Args) > 0 {
		_, err := fmt.Fprintf(w, " %s", strings.Join(plan.Args, " "))
		if err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

func printShellExports(w io.Writer, env map[string]string) error {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := fmt.Fprintf(w, "export %s=%s\n", key, shellQuote(env[key])); err != nil {
			return err
		}
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func redactLaunchEnvValue(key string, value string) string {
	upperKey := strings.ToUpper(key)
	if strings.Contains(upperKey, "TOKEN") || strings.Contains(upperKey, "KEY") || strings.Contains(upperKey, "SECRET") || strings.Contains(upperKey, "PASSWORD") {
		return "<redacted>"
	}
	return value
}

func mergeEnv(base []string, extra map[string]string) []string {
	merged := make([]string, 0, len(base)+len(extra))
	seen := make(map[string]struct{}, len(extra))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if value, replace := extra[key]; replace {
				merged = append(merged, key+"="+value)
				seen[key] = struct{}{}
				continue
			}
		}
		merged = append(merged, item)
	}
	for key, value := range extra {
		if _, ok := seen[key]; ok {
			continue
		}
		merged = append(merged, key+"="+value)
	}
	return merged
}
