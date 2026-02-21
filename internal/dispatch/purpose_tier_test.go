package dispatch

import (
	"testing"

	"github.com/antigravity-dev/chum/internal/config"
)

func TestPreferredTiersForPurpose(t *testing.T) {
	tests := []struct {
		purpose string
		want    []string
	}{
		{purpose: "planning", want: []string{"premium", "balanced", "fast"}},
		{purpose: "review", want: []string{"balanced", "premium", "fast"}},
		{purpose: "reporting", want: []string{"fast", "balanced", "premium"}},
		{purpose: "unknown", want: []string{"balanced", "fast", "premium"}},
	}

	for _, tt := range tests {
		got := PreferredTiersForPurpose(tt.purpose)
		if len(got) != len(tt.want) {
			t.Fatalf("PreferredTiersForPurpose(%q) len=%d want=%d", tt.purpose, len(got), len(tt.want))
		}
		for i := range tt.want {
			if got[i] != tt.want[i] {
				t.Fatalf("PreferredTiersForPurpose(%q)[%d]=%q want=%q", tt.purpose, i, got[i], tt.want[i])
			}
		}
	}
}

func TestSelectProviderForPurpose_FallbackByTier(t *testing.T) {
	cfg := &config.Config{
		Tiers: config.Tiers{
			Fast:     []string{"fast-provider"},
			Balanced: []string{"balanced-provider"},
			Premium:  []string{"premium-provider"},
		},
		Providers: map[string]config.Provider{
			"fast-provider":     {Model: "fast-model"},
			"balanced-provider": {Model: "balanced-model"},
		},
	}

	model, tier := SelectProviderForPurpose(cfg, ScrumPurposePlanning)
	if model != "balanced-model" || tier != "balanced" {
		t.Fatalf("planning fallback got (%q,%q), want (balanced-model,balanced)", model, tier)
	}

	model, tier = SelectProviderForPurpose(cfg, ScrumPurposeReporting)
	if model != "fast-model" || tier != "fast" {
		t.Fatalf("reporting fallback got (%q,%q), want (fast-model,fast)", model, tier)
	}
}

func TestSelectProviderForPurpose_NoProviders(t *testing.T) {
	cfg := &config.Config{
		Tiers: config.Tiers{
			Fast:     []string{"missing"},
			Balanced: []string{"also-missing"},
			Premium:  []string{"still-missing"},
		},
		Providers: map[string]config.Provider{},
	}

	model, tier := SelectProviderForPurpose(cfg, ScrumPurposeReview)
	if model != "" || tier != "" {
		t.Fatalf("expected empty selection, got (%q,%q)", model, tier)
	}
}
