package main

import (
	"strings"
	"testing"
)

func TestBuildStartupBannerDefaults(t *testing.T) {
	got := buildStartupBanner(startupBannerData{
		Tagline: "AI Gateway / Semantic Router",
		Listens: []string{":8080"},
	})

	checks := []string{
		"____                   ____",
		"AI Gateway / Semantic Router",
		"version: dev",
		"listen: :8080",
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
		TraceMode:     "full",
	})

	checks := []string{
		"version: 1.2.3",
		"listen: :8080, :9090",
		"dev-log: on (tmp/dev.log)",
		"trace: full",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("banner = %q, want substring %q", got, want)
		}
	}
}
