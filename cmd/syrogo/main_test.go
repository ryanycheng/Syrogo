package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitCommandArgsPassesLeadingConfigToRun(t *testing.T) {
	command, args, ok := splitCommandArgs([]string{"--config", "/tmp/syrogo.yaml", "run", "claude", "--model", "claude-sonnet-4-6"})
	if !ok {
		t.Fatal("splitCommandArgs() ok = false, want true")
	}
	if command != "run" {
		t.Fatalf("command = %q, want run", command)
	}
	want := []string{"--config", "/tmp/syrogo.yaml", "claude", "--model", "claude-sonnet-4-6"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestSplitCommandArgsSupportsConfigEquals(t *testing.T) {
	command, args, ok := splitCommandArgs([]string{"--config=/tmp/syrogo.yaml", "activate", "codex"})
	if !ok {
		t.Fatal("splitCommandArgs() ok = false, want true")
	}
	if command != "activate" {
		t.Fatalf("command = %q, want activate", command)
	}
	want := []string{"--config", "/tmp/syrogo.yaml", "codex"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestSplitCommandArgsIgnoresServiceArgs(t *testing.T) {
	_, _, ok := splitCommandArgs([]string{"--config", "/tmp/syrogo.yaml", "--dev-log"})
	if ok {
		t.Fatal("splitCommandArgs() ok = true, want false")
	}
}

func TestIsVersionCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "long flag", args: []string{"--version"}, want: true},
		{name: "short flag", args: []string{"-version"}, want: true},
		{name: "subcommand", args: []string{"version"}, want: true},
		{name: "empty", args: nil},
		{name: "service flags", args: []string{"--config", "config.yaml"}},
		{name: "existing subcommand", args: []string{"run", "claude"}},
		{name: "extra argument", args: []string{"version", "extra"}},
		{name: "mixed flags", args: []string{"--config", "config.yaml", "--version"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isVersionCommand(test.args); got != test.want {
				t.Fatalf("isVersionCommand(%q) = %t, want %t", test.args, got, test.want)
			}
		})
	}
}

func TestFormatVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "release", version: "v0.16.3", want: "syrogo v0.16.3"},
		{name: "development", version: "dev", want: "syrogo dev"},
		{name: "empty", want: "syrogo dev"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatVersion(test.version); got != test.want {
				t.Fatalf("formatVersion(%q) = %q, want %q", test.version, got, test.want)
			}
		})
	}
}

func TestBuildStartupBannerDefaults(t *testing.T) {
	got := buildStartupBanner(startupBannerData{
		Tagline: "AI Gateway / Semantic Router",
		Listens: []string{":23234"},
	})

	checks := []string{
		"____                   ____",
		"AI Gateway / Semantic Router",
		"version: dev",
		"listen: :23234",
		"dev-log: off",
		"trace: off",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("banner = %q, want substring %q", got, want)
		}
	}
}

func TestBuildStartupBannerWithMultipleListenersAndFlags(t *testing.T) {
	got := buildStartupBanner(startupBannerData{
		Version:       "1.2.3",
		Tagline:       "AI Gateway / Semantic Router",
		Listens:       []string{":8080", ":9090"},
		DevLogEnabled: true,
		DevLogPath:    "/var/log/syrogo/custom.log",
		TraceMode:     "full",
	})

	checks := []string{
		"version: 1.2.3",
		"listen: :8080, :9090",
		"dev-log: on (/var/log/syrogo/custom.log)",
		"trace: full",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("banner = %q, want substring %q", got, want)
		}
	}
}
