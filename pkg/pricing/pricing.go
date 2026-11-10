// Package pricing turns per-message token counts into rough USD estimates.
//
// Rates are approximate list prices per million tokens. They're a moving
// target — treat these numbers as "order of magnitude" and update the table
// when Anthropic publishes new pricing. Users who want exact accounting can
// override via CT_PRICING env (path to a JSON file with the same shape).
package pricing

import (
	"encoding/json"
	"os"
	"strings"
)

// Rates in dollars per million tokens.
type Rates struct {
	Input           float64 `json:"input"`
	Output          float64 `json:"output"`
	CacheRead       float64 `json:"cacheRead"`
	CacheCreation5m float64 `json:"cacheCreation5m"`
}

// Default table — matches Anthropic's list prices for Claude 4.x as of 2026.
var defaultTable = map[string]Rates{
	"claude-opus-4":   {Input: 15, Output: 75, CacheRead: 1.50, CacheCreation5m: 18.75},
	"claude-sonnet-4": {Input: 3, Output: 15, CacheRead: 0.30, CacheCreation5m: 3.75},
	"claude-haiku-4":  {Input: 1, Output: 5, CacheRead: 0.10, CacheCreation5m: 1.25},
	"unknown":         {Input: 3, Output: 15, CacheRead: 0.30, CacheCreation5m: 3.75}, // Sonnet as fallback
}

var table = loadTable()

func loadTable() map[string]Rates {
	t := map[string]Rates{}
	for k, v := range defaultTable {
		t[k] = v
	}
	if path := os.Getenv("CT_PRICING"); path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			var override map[string]Rates
			if err := json.Unmarshal(b, &override); err == nil {
				for k, v := range override {
					t[k] = v
				}
			}
		}
	}
	return t
}

// RatesFor returns pricing for a model id. Matches loose prefixes so
// "claude-opus-4-7-1m" resolves to "claude-opus-4".
func RatesFor(model string) Rates {
	if model == "" {
		return table["unknown"]
	}
	best := table["unknown"]
	for prefix, r := range table {
		if prefix == "unknown" {
			continue
		}
		if strings.HasPrefix(model, prefix) {
			best = r
		}
	}
	return best
}

// CostUSD computes an estimate given raw token counts. All arguments are the
// numeric counts (not fractions of a million).
func CostUSD(model string, input, output, cacheRead, cacheCreation int64) float64 {
	r := RatesFor(model)
	toM := func(n int64) float64 { return float64(n) / 1_000_000 }
	return toM(input)*r.Input +
		toM(output)*r.Output +
		toM(cacheRead)*r.CacheRead +
		toM(cacheCreation)*r.CacheCreation5m
}
