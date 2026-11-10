package pricing

import (
	"math"
	"testing"
)

func TestCostUSD(t *testing.T) {
	// 1M of each bucket at Sonnet 4 rates: 3 + 15 + 0.30 + 3.75.
	got := CostUSD("claude-sonnet-4-5-20250929", 1_000_000, 1_000_000, 1_000_000, 1_000_000)
	if math.Abs(got-22.05) > 1e-9 {
		t.Fatalf("got %v", got)
	}
	if RatesFor("claude-opus-4-1") != RatesFor("claude-opus-4") {
		t.Fatal("prefix match failed")
	}
	if RatesFor("") != RatesFor("something-else") {
		t.Fatal("unknown model should fall back")
	}
}
