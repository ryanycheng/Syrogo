package accounting

import (
	"testing"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

func TestBuiltInPriceSnapshotMetadata(t *testing.T) {
	if builtInPriceSnapshot.Source.Repository != "BerriAI/litellm" {
		t.Fatalf("repository = %q, want BerriAI/litellm", builtInPriceSnapshot.Source.Repository)
	}
	if builtInPriceSnapshot.Source.Revision != "49ca04d8c3ddea336237ce6f3082dbc26d19e944" {
		t.Fatalf("revision = %q, want locked LiteLLM revision", builtInPriceSnapshot.Source.Revision)
	}
	if len(builtInPriceSnapshot.Items) < 20 {
		t.Fatalf("items = %d, want at least 20", len(builtInPriceSnapshot.Items))
	}
}

func TestPriceCalculatorUsesCurrentOpenAICodexDefaultPrice(t *testing.T) {
	calculator := NewPriceCalculator(nil)
	cost := calculator.CostUSD("openai", "gpt-5.3-codex", runtime.UsageBreakdown{
		InputTokens:           1_000_000,
		OutputTokens:          1_000_000,
		CachedInputReadTokens: 1_000_000,
	})
	if cost != 15.925 {
		t.Fatalf("cost = %v, want 15.925", cost)
	}
}

func TestPriceCalculatorUsesCurrentAnthropicDefaultPrice(t *testing.T) {
	calculator := NewPriceCalculator(nil)
	cost := calculator.CostUSD("anthropic", "claude-opus-4-7", runtime.UsageBreakdown{
		InputTokens:            1_000_000,
		OutputTokens:           1_000_000,
		CachedInputWriteTokens: 1_000_000,
		CachedInputReadTokens:  1_000_000,
	})
	if cost != 36.75 {
		t.Fatalf("cost = %v, want 36.75", cost)
	}
}

func TestPriceCalculatorUsesDefaultModelPrice(t *testing.T) {
	calculator := NewPriceCalculator(nil)
	cost := calculator.CostUSD("custom-openai", "gpt-4o-mini", runtime.UsageBreakdown{
		InputTokens:           1_000_000,
		OutputTokens:          1_000_000,
		CachedInputReadTokens: 1_000_000,
	})
	if cost != 0.825 {
		t.Fatalf("cost = %v, want 0.825", cost)
	}
}

func TestPriceCalculatorFallsBackFromQualifiedModelToModelOnlyPrice(t *testing.T) {
	calculator := NewPriceCalculator(nil)
	cost := calculator.CostUSD("custom-openai", "openai/gpt-4o-mini", runtime.UsageBreakdown{
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	})
	if cost != 0.75 {
		t.Fatalf("cost = %v, want 0.75", cost)
	}
}

func TestPriceCalculatorQualifiedExactPriceTakesPriority(t *testing.T) {
	calculator := NewPriceCalculator([]config.AccountingPriceConfig{{
		Model:               "gpt-4o-mini",
		InputPerMillionUSD:  1,
		OutputPerMillionUSD: 2,
	}, {
		Provider:            "custom-openai",
		Model:               "openai/gpt-4o-mini",
		InputPerMillionUSD:  3,
		OutputPerMillionUSD: 4,
	}})
	cost := calculator.CostUSD("custom-openai", "openai/gpt-4o-mini", runtime.UsageBreakdown{
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	})
	if cost != 7 {
		t.Fatalf("cost = %v, want 7", cost)
	}
}

func TestPriceCalculatorUserModelPriceOverridesDefault(t *testing.T) {
	calculator := NewPriceCalculator([]config.AccountingPriceConfig{{
		Model:                  "gpt-4o-mini",
		InputPerMillionUSD:     1,
		OutputPerMillionUSD:    2,
		CacheReadPerMillionUSD: 0.5,
	}})
	cost := calculator.CostUSD("custom-openai", "gpt-4o-mini", runtime.UsageBreakdown{
		InputTokens:           1_000_000,
		OutputTokens:          1_000_000,
		CachedInputReadTokens: 1_000_000,
	})
	if cost != 3.5 {
		t.Fatalf("cost = %v, want 3.5", cost)
	}
}

func TestPriceCalculatorUserProviderPriceOverridesModelPrice(t *testing.T) {
	calculator := NewPriceCalculator([]config.AccountingPriceConfig{{
		Model:               "custom-model",
		InputPerMillionUSD:  1,
		OutputPerMillionUSD: 2,
	}, {
		Provider:            "openai-main",
		Model:               "custom-model",
		InputPerMillionUSD:  3,
		OutputPerMillionUSD: 4,
	}})
	cost := calculator.CostUSD("openai-main", "custom-model", runtime.UsageBreakdown{
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	})
	if cost != 7 {
		t.Fatalf("cost = %v, want 7", cost)
	}
}
