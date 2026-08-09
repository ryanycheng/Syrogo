package accounting

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/ryanycheng/Syrogo/internal/config"
	"github.com/ryanycheng/Syrogo/internal/runtime"
)

type defaultPriceSnapshot struct {
	Source struct {
		Repository string `json:"repository"`
		Revision   string `json:"revision"`
		Path       string `json:"path"`
		UpdatedAt  string `json:"updated_at"`
	} `json:"source"`
	Items []config.AccountingPriceConfig `json:"items"`
}

//go:embed pricing_default.json
var defaultPriceSnapshotJSON []byte

var builtInPriceSnapshot = loadDefaultPriceSnapshot()

type PriceCalculator struct {
	items []config.AccountingPriceConfig
}

func NewPriceCalculator(items []config.AccountingPriceConfig) PriceCalculator {
	merged := append([]config.AccountingPriceConfig(nil), builtInPriceSnapshot.Items...)
	merged = append(merged, items...)
	return PriceCalculator{items: merged}
}

func (c PriceCalculator) CostUSD(provider, model string, usage runtime.UsageBreakdown) float64 {
	cost, _ := c.CostUSDWithMatch(provider, model, usage)
	return cost
}

func (c PriceCalculator) CostUSDWithMatch(provider, model string, usage runtime.UsageBreakdown) (float64, bool) {
	price, ok := c.match(provider, model)
	if !ok {
		return 0, false
	}
	return perMillion(usage.InputTokens, price.InputPerMillionUSD) +
		perMillion(usage.OutputTokens, price.OutputPerMillionUSD) +
		perMillion(usage.CachedInputWriteTokens, price.CacheCreatePerMillionUSD) +
		perMillion(usage.CachedInputReadTokens, price.CacheReadPerMillionUSD), true
}

func (c PriceCalculator) match(provider, model string) (config.AccountingPriceConfig, bool) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if price, ok := c.matchExact(provider, model); ok {
		return price, true
	}
	if separator := strings.LastIndex(model, "/"); separator >= 0 {
		return c.matchExact("", strings.TrimSpace(model[separator+1:]))
	}
	return config.AccountingPriceConfig{}, false
}

func (c PriceCalculator) matchExact(provider, model string) (config.AccountingPriceConfig, bool) {
	var modelOnly config.AccountingPriceConfig
	modelOnlyOK := false
	for i := len(c.items) - 1; i >= 0; i-- {
		item := c.items[i]
		if item.Model != model {
			continue
		}
		if item.Provider == provider {
			return item, true
		}
		if item.Provider == "" && !modelOnlyOK {
			modelOnly = item
			modelOnlyOK = true
		}
	}
	return modelOnly, modelOnlyOK
}

func loadDefaultPriceSnapshot() defaultPriceSnapshot {
	var snapshot defaultPriceSnapshot
	if err := json.Unmarshal(defaultPriceSnapshotJSON, &snapshot); err != nil {
		panic("invalid embedded pricing snapshot: " + err.Error())
	}
	if snapshot.Source.Repository == "" || snapshot.Source.Revision == "" || len(snapshot.Items) == 0 {
		panic("invalid embedded pricing snapshot metadata")
	}
	return snapshot
}

func perMillion(tokens int, rate float64) float64 {
	if tokens <= 0 || rate <= 0 {
		return 0
	}
	return float64(tokens) * rate / 1_000_000
}
