package discordbot

import (
	"path/filepath"
	"testing"

	appconfig "spar-tilbuds-bot/internal/config"
	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/store"

	"github.com/bwmarrin/discordgo"
	"tjekapi"
)

func TestFeedsFromConfig(t *testing.T) {
	feeds := feedsFromConfig(appconfig.BotConfig{ChannelID: 1, PingRoleID: 2})
	if len(feeds) != 1 || feeds[0].retailer != "spar" {
		t.Fatalf("spar-only = %+v", feeds)
	}
	feeds = feedsFromConfig(appconfig.BotConfig{ChannelID: 1, FotexChannelID: 3, FotexPingRoleID: 4})
	if len(feeds) != 2 || feeds[1].retailer != "fotex" || feeds[1].pingRoleID != 4 {
		t.Fatalf("both feeds = %+v", feeds)
	}
	feeds = feedsFromConfig(appconfig.BotConfig{ChannelID: 1, FotexChannelID: 3, FotexPingRoleID: 4, LidlChannelID: 5, LidlPingRoleID: 6})
	if len(feeds) != 3 || feeds[2].retailer != "lidl" || feeds[2].channelID != 5 || feeds[2].pingRoleID != 6 {
		t.Fatalf("lidl feed = %+v", feeds)
	}
	if got := retailerDisplay("fotex"); got != "Føtex" {
		t.Errorf("fotex display = %q", got)
	}
	if got := retailerDisplay("lidl"); got != "Lidl" {
		t.Errorf("lidl display = %q", got)
	}
	if got := retailerDisplay("spar"); got != "SPAR" {
		t.Errorf("spar display = %q", got)
	}
}

func TestSelectFeeds(t *testing.T) {
	config := appconfig.BotConfig{ChannelID: 1, FotexChannelID: 2}
	if got := selectFeeds(config, "alle"); len(got) != 2 {
		t.Errorf("alle = %d feeds, want 2", len(got))
	}
	if got := selectFeeds(config, "fotex"); len(got) != 1 || got[0].retailer != "fotex" {
		t.Errorf("fotex = %+v", got)
	}
	if got := selectFeeds(config, "netto"); len(got) != 0 {
		t.Errorf("unknown retailer = %+v, want none", got)
	}
}

func TestRetailerOption(t *testing.T) {
	data := discordgo.ApplicationCommandInteractionData{}
	if got := retailerOption(data, "spar"); got != "spar" {
		t.Errorf("missing option = %q", got)
	}
	if got := retailerOption(data, "alle"); got != "alle" {
		t.Errorf("missing option alle = %q", got)
	}
	withChoice := discordgo.ApplicationCommandInteractionData{
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "butik", Type: discordgo.ApplicationCommandOptionString, Value: "fotex"},
		},
	}
	if got := retailerOption(withChoice, "spar"); got != "fotex" {
		t.Errorf("fotex choice = %q", got)
	}
	withBogus := discordgo.ApplicationCommandInteractionData{
		Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "butik", Type: discordgo.ApplicationCommandOptionString, Value: "netto"},
		},
	}
	if got := retailerOption(withBogus, "spar"); got != "spar" {
		t.Errorf("bogus choice = %q, want default", got)
	}
}

func TestFilterUnseenDeals(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bot := &DiscordBot{db: db}
	deals := []match.ScoredDeal{
		{Deal: tjekapi.Deal{OfferID: "a"}},
		{Deal: tjekapi.Deal{OfferID: "b"}},
	}
	if got := bot.filterUnseenDeals("spar", "pub1", deals); len(got) != 2 {
		t.Fatalf("no store history: %d deals, want 2", len(got))
	}
	if err := db.AddPostedDeals("spar", "pub1", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	got := bot.filterUnseenDeals("spar", "pub1", deals)
	if len(got) != 1 || got[0].Deal.OfferID != "b" {
		t.Errorf("after posting a: %+v, want only b", got)
	}
	// Other retailer unaffected.
	if got := bot.filterUnseenDeals("fotex", "pub1", deals); len(got) != 2 {
		t.Errorf("fotex feed: %d deals, want 2", len(got))
	}
	// No database: legacy all-unseen behavior.
	plain := &DiscordBot{}
	if got := plain.filterUnseenDeals("spar", "pub1", deals); len(got) != 2 {
		t.Errorf("nil store: %d deals, want 2", len(got))
	}
}

func TestTagRetailer(t *testing.T) {
	got := tagRetailer(appconfig.PostedMessage{ChannelID: "1", MessageID: "2"}, "fotex")
	if got.Retailer != "fotex" || got.ChannelID != "1" {
		t.Errorf("tagged = %+v", got)
	}
}
