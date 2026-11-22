package discordbot

import (
	"strings"
	"testing"

	"spar-tilbuds-bot/internal/match"
	"tjekapi"
)

func TestBuildPreviewContentShowsScoresAndReasons(t *testing.T) {
	price := 18.0
	deals := []match.ScoredDeal{
		{
			Deal:   tjekapi.Deal{CleanName: "Kims Chips", Price: &price, MatchingKeywords: []string{"chips"}},
			WantID: "chips",
			Score:  match.ScoreExact,
			Reason: `ord "chips"`,
		},
		{
			Deal:   tjekapi.Deal{CleanName: "Pepsi Max", Price: &price, IsAppPrice: true},
			WantID: "pepsi",
			Score:  match.ScoreExact,
			Reason: `ord "pepsi"`,
		},
	}

	content := buildPreviewContent(deals, 10)
	for _, want := range []string{"Kims Chips", "18 kr", "[chips · 1.00", "Pepsi Max", "(app-pris)", "pepsi · 1.00"} {
		if !strings.Contains(content, want) {
			t.Errorf("preview content %q misses %q", content, want)
		}
	}

	if got := buildPreviewContent(nil, 10); got != "Ingen matchende tilbud fundet." {
		t.Errorf("empty preview = %q", got)
	}
}
