package config

import (
	"path/filepath"
	"reflect"
	"testing"

	"spar-tilbuds-bot/internal/match"
)

func TestConfigStoreLoadSaveRoundTrip(t *testing.T) {
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.toml"))
	maxPrice := 20.0
	allowApp := false
	want := BotConfig{
		ChannelID:        100000000000000001,
		LastWeek:         `pub-"42"`,
		PingRoleID:       100000000000000002,
		CheckCron:        "*/30 * * * 5",
		NotificationTime: "07:00",
		Matcher:          "normalized",
		Keywords:         []string{"pepsi", "chips"},
		Wants: []match.Want{
			{
				ID:            "sodavand",
				Describe:      "Sodavand til weekenden",
				Include:       []string{"sodavand", "cola"},
				Exclude:       []string{"lakrids"},
				MaxPrice:      &maxPrice,
				AllowAppPrice: &allowApp,
				Threshold:     0.6,
				Retailers:     []string{"spar", "fotex"},
			},
		},
		PostedMessages: []PostedMessage{
			{Retailer: "spar", ChannelID: "111", MessageID: "222"},
		},
	}

	if err := store.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestConfigStoreUpdateCreatesFile(t *testing.T) {
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.toml"))

	got, err := store.Update(func(config *BotConfig) {
		config.ChannelID = 123
		config.LastWeek = "publication"
		config.PingRoleID = 456
		config.CheckCron = "0 9 * * 5"
		config.NotificationTime = "09:30"
		config.Keywords = []string{"oreo"}
		config.PostedMessages = []PostedMessage{{Retailer: "spar", ChannelID: "789", MessageID: "101112"}}
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got.ChannelID != 123 || got.LastWeek != "publication" || got.PingRoleID != 456 ||
		got.CheckCron != "0 9 * * 5" || got.NotificationTime != "09:30" {
		t.Fatalf("Update() = %+v", got)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, got) {
		t.Fatalf("Load() = %+v, want %+v", loaded, got)
	}
}

func TestEffectiveWantsFallsBackToKeywords(t *testing.T) {
	config := BotConfig{Keywords: []string{"pepsi", "chips"}}
	wants := config.EffectiveWants()
	if len(wants) != 2 || wants[0].ID != "pepsi" || wants[1].ID != "chips" {
		t.Fatalf("EffectiveWants() = %+v, want one want per keyword", wants)
	}

	config = BotConfig{
		Keywords: []string{"pepsi"},
		Wants:    []match.Want{{ID: "sodavand", Include: []string{"sodavand"}}},
	}
	wants = config.EffectiveWants()
	if len(wants) != 1 || wants[0].ID != "sodavand" {
		t.Fatalf("EffectiveWants() = %+v, want the configured wants", wants)
	}
}

func TestPostedMessagesRetailerFormat(t *testing.T) {
	parsed := parsePostedMessages([]string{
		"spar:111:222",
		"fotex:333:444",
		"555:666",
		"broken",
		"a:b:c:d",
		":111:222",
	})
	if len(parsed) != 3 {
		t.Fatalf("parsed = %+v, want 3", parsed)
	}
	if parsed[0].Retailer != "spar" || parsed[1].Retailer != "fotex" || parsed[2].Retailer != "spar" {
		t.Errorf("retailers = %+v, want spar/fotex/legacy-spar", parsed)
	}
	filtered := FilterPostedMessages(parsed, "fotex")
	if len(filtered) != 1 || filtered[0].MessageID != "444" {
		t.Errorf("fotex filter = %+v", filtered)
	}
	if got := formatPostedMessages(parsed); len(got) != 3 || got[0] != "spar:111:222" || got[2] != "spar:555:666" {
		t.Errorf("formatted = %v", got)
	}
}

func TestFotexFieldsRoundTrip(t *testing.T) {
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.toml"))
	want := BotConfig{FotexChannelID: 111, FotexLastWeek: "fotex-pub", FotexPingRoleID: 222}
	if _, err := store.Update(func(config *BotConfig) {
		config.FotexChannelID = 111
		config.FotexLastWeek = "fotex-pub"
		config.FotexPingRoleID = 222
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.FotexChannelID != want.FotexChannelID || got.FotexLastWeek != want.FotexLastWeek || got.FotexPingRoleID != want.FotexPingRoleID {
		t.Errorf("fotex fields = %+v, want %+v", got, want)
	}
}

func TestLidlFieldsRoundTrip(t *testing.T) {
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.toml"))
	want := BotConfig{LidlChannelID: 333, LidlLastWeek: "lidl-pub", LidlPingRoleID: 444}
	if _, err := store.Update(func(config *BotConfig) {
		config.LidlChannelID = 333
		config.LidlLastWeek = "lidl-pub"
		config.LidlPingRoleID = 444
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.LidlChannelID != want.LidlChannelID || got.LidlLastWeek != want.LidlLastWeek || got.LidlPingRoleID != want.LidlPingRoleID {
		t.Errorf("lidl fields = %+v, want %+v", got, want)
	}
}
