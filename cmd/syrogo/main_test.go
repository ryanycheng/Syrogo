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
