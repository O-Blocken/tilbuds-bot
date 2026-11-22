package discordbot

import (
	"testing"

	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/service"
	"spar-tilbuds-bot/internal/store"

	"tjekapi"
)

func alertMatch(name string, price float64) service.AlertHit {
	return service.AlertHit{
		QueryIndex: 0,
		Scored: match.ScoredDeal{
			Deal: tjekapi.Deal{
				CleanName: name,
				Price:     &price,
			},
		},
	}
}

// TestBestAlertHitTargetMode fires at or below the target price and
// picks the cheapest match otherwise.
func TestBestAlertHitTargetMode(t *testing.T) {
	b := &DiscordBot{}
	target := 10.0
	row := store.PriceAlertRow{ID: 1, Query: "øl", TargetPrice: &target}

	hit, ok := b.bestAlertHit(row, "spar", "pub-1", []service.AlertHit{
		alertMatch("Dyr øl", 12),
		alertMatch("Billig øl", 9),
	})
	if !ok {
		t.Fatal("target-priced alert must fire at or below target")
	}
	if hit.name != "Billig øl" || hit.price != 9 {
		t.Fatalf("cheapest match wins, got %+v", hit)
	}

	hit, ok = b.bestAlertHit(row, "spar", "pub-1", []service.AlertHit{
		alertMatch("Dyr øl", 12),
	})
	if ok {
		t.Fatalf("above-target alert must not fire, got %+v", hit)
	}
	if _, ok := b.bestAlertHit(row, "spar", "pub-1", nil); ok {
		t.Fatal("no matches must not fire")
	}
}

// TestBestAlertHitUnknownPriceNeverFires guards the zero-price case:
// deals without a parseable price must never trigger an alert.
func TestBestAlertHitUnknownPriceNeverFires(t *testing.T) {
	b := &DiscordBot{}
	target := 10.0
	row := store.PriceAlertRow{ID: 1, Query: "øl", TargetPrice: &target}
	if _, ok := b.bestAlertHit(row, "spar", "pub-1", []service.AlertHit{alertMatch("Uden pris", 0)}); ok {
		t.Fatal("zero price must never fire")
	}
}

// TestPrisalarmPriceFormatting checks the Danish decimal comma.
func TestPrisalarmPriceFormatting(t *testing.T) {
	if got := formatAlarmPrice(12.5); got != "12,5" {
		t.Fatalf("formatAlarmPrice(12.5) = %q", got)
	}
	if got := formatAlarmPrice(99); got != "99" {
		t.Fatalf("formatAlarmPrice(99) = %q", got)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList(" pilsner , lager ,,øl")
	want := []string{"pilsner", "lager", "øl"}
	if len(got) != len(want) {
		t.Fatalf("splitList() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitList()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(splitList("")) != 0 {
		t.Fatal("empty input must yield no items")
	}
}
