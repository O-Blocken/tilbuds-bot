package discordbot

import (
	"path/filepath"
	"strings"
	"testing"

	appconfig "spar-tilbuds-bot/internal/config"
	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/store"

	"tjekapi"
)

func TestWantIDPattern(t *testing.T) {
	valid := []string{"sodavand", "fredagsslik-2", "a", "x_y-9"}
	for _, id := range valid {
		if !wantIDPattern.MatchString(id) {
			t.Errorf("id %q should be valid", id)
		}
	}
	invalid := []string{"", "Sodavand", "sodavand!", "a b", strings.Repeat("a", 41)}
	for _, id := range invalid {
		if wantIDPattern.MatchString(id) {
			t.Errorf("id %q should be rejected", id)
		}
	}
}

func TestLoadWantsPrefersStore(t *testing.T) {
	bot := &DiscordBot{}
	config := appconfig.BotConfig{Keywords: []string{"pepsi"}}
	wants, err := bot.loadWants(config)
	if err != nil || len(wants) != 1 || wants[0].ID != "pepsi" {
		t.Fatalf("TOML fallback = %+v %v", wants, err)
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bot.SetStore(db)
	// Empty store still falls back to TOML.
	wants, err = bot.loadWants(config)
	if err != nil || len(wants) != 1 {
		t.Fatalf("empty store fallback = %+v %v", wants, err)
	}
	if err := db.UpsertWant(store.WantRow{ID: "sodavand", Includes: []string{"cola"}}); err != nil {
		t.Fatal(err)
	}
	wants, err = bot.loadWants(config)
	if err != nil || len(wants) != 1 || wants[0].ID != "sodavand" {
		t.Fatalf("store wants = %+v %v", wants, err)
	}
}

func TestBuildTopContent(t *testing.T) {
	price := 59.0
	deals := []match.ScoredDeal{
		{Deal: dealWithPrice("Pepsi Max", price), WantID: "sodavand", Score: 0.9, Final: 0.85,
			Badges: []string{"billigste i 30 dage"}},
	}
	content := buildTopContent(deals)
	for _, want := range []string{"Ugens top", "Pepsi Max", "59 kr", "0.85", "billigste i 30 dage"} {
		if !strings.Contains(content, want) {
			t.Errorf("top content misses %q:\n%s", want, content)
		}
	}
	if got := buildTopContent(nil); got != "Ingen rangerede tilbud fundet." {
		t.Errorf("empty top = %q", got)
	}
}

func TestHasLowestEverBadge(t *testing.T) {
	if !hasLowestEverBadge([]string{"god deal", "billigste i 30 dage"}) {
		t.Error("should detect spotlight badge")
	}
	if hasLowestEverBadge([]string{"billigste lige nu"}) {
		t.Error("must not confuse cheapest-now with lowest-ever")
	}
	if hasLowestEverBadge(nil) {
		t.Error("nil badges must not spotlight")
	}
}

func dealWithPrice(name string, price float64) tjekapi.Deal {
	return tjekapi.Deal{CleanName: name, Price: &price}
}
