package discordbot

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	appconfig "spar-tilbuds-bot/internal/config"

	"github.com/bwmarrin/discordgo"
)

func TestRemoveTrackedMessagesKeepsOtherFeeds(t *testing.T) {
	stored := []appconfig.PostedMessage{
		{ChannelID: "chan-spar", MessageID: "1", Retailer: "spar"},
		{ChannelID: "chan-fotex", MessageID: "2", Retailer: "fotex"},
		{ChannelID: "chan-spar", MessageID: "3", Retailer: "spar"},
	}
	remove := []appconfig.PostedMessage{
		{ChannelID: "chan-spar", MessageID: "1", Retailer: "spar"},
		{ChannelID: "chan-spar", MessageID: "3", Retailer: "spar"},
	}
	kept := removeTrackedMessages(stored, remove)
	if len(kept) != 1 || kept[0].MessageID != "2" || kept[0].Retailer != "fotex" {
		t.Fatalf("kept = %+v, want only the fotex entry", kept)
	}
}

func TestRemoveTrackedMessagesEdgeCases(t *testing.T) {
	stored := []appconfig.PostedMessage{
		{ChannelID: "chan", MessageID: "1"},
		{ChannelID: "", MessageID: ""},
	}
	// Empty removal keeps everything, including malformed entries.
	if kept := removeTrackedMessages(stored, nil); len(kept) != 2 {
		t.Fatalf("empty remove kept %+v, want all stored", kept)
	}
	// Removal matches exact entries only.
	kept := removeTrackedMessages(stored, []appconfig.PostedMessage{{ChannelID: "chan", MessageID: "1"}})
	if len(kept) != 1 || kept[0].MessageID != "" {
		t.Fatalf("kept = %+v, want only the malformed entry", kept)
	}
	// Unknown IDs change nothing.
	if kept := removeTrackedMessages(stored, []appconfig.PostedMessage{{ChannelID: "nope", MessageID: "nope"}}); len(kept) != 2 {
		t.Fatalf("unknown remove kept %+v, want all stored", kept)
	}
}

func TestIsMissingDiscordMessage(t *testing.T) {
	missing := &discordgo.RESTError{Response: &http.Response{StatusCode: http.StatusNotFound}}
	if !isMissingDiscordMessage(missing) {
		t.Error("404 RESTError should count as missing")
	}
	forbidden := &discordgo.RESTError{Response: &http.Response{StatusCode: http.StatusForbidden}}
	if isMissingDiscordMessage(forbidden) {
		t.Error("403 RESTError must not count as missing")
	}
	if isMissingDiscordMessage(&discordgo.RESTError{}) {
		t.Error("RESTError without response must not count as missing")
	}
	if isMissingDiscordMessage(errors.New("boom")) {
		t.Error("plain error must not count as missing")
	}
}

// rewriteTransport redirects Discord API calls to a test server,
// preserving path and query.
type rewriteTransport struct {
	target string
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	base, err := url.Parse(t.target)
	if err != nil {
		return nil, err
	}
	out.URL.Scheme = base.Scheme
	out.URL.Host = base.Host
	return http.DefaultTransport.RoundTrip(out)
}

// discordTestBot returns a bot whose Discord session talks to server and
// whose config store starts with the given tracked messages.
func discordTestBot(t *testing.T, server *httptest.Server, tracked []appconfig.PostedMessage) *DiscordBot {
	t.Helper()
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = &http.Client{Transport: rewriteTransport{target: server.URL}}
	configStore := appconfig.NewConfigStore(filepath.Join(t.TempDir(), "config.toml"))
	if err := configStore.Save(appconfig.BotConfig{PostedMessages: tracked}); err != nil {
		t.Fatal(err)
	}
	return &DiscordBot{session: session, configStore: configStore}
}

func storedMessages(t *testing.T, bot *DiscordBot) []appconfig.PostedMessage {
	t.Helper()
	config, err := bot.configStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	return config.PostedMessages
}

func TestClearTrackedMessagesDropsFailuresAndKeepsOtherFeeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch {
		case strings.HasSuffix(req.URL.Path, "/denied"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message": "Missing Permissions", "code": 50013}`))
		case strings.HasSuffix(req.URL.Path, "/gone"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message": "Unknown Message", "code": 50007}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	bot := discordTestBot(t, server, []appconfig.PostedMessage{
		{ChannelID: "chan", MessageID: "ok", Retailer: "spar"},
		{ChannelID: "chan", MessageID: "gone", Retailer: "spar"},
		{ChannelID: "chan", MessageID: "denied", Retailer: "spar"},
		{ChannelID: "other", MessageID: "keep", Retailer: "fotex"},
	})

	failures, err := bot.clearTrackedMessages([]appconfig.PostedMessage{
		{ChannelID: "chan", MessageID: "ok", Retailer: "spar"},
		{ChannelID: "chan", MessageID: "gone", Retailer: "spar"},
		{ChannelID: "chan", MessageID: "denied", Retailer: "spar"},
	})
	if err != nil {
		t.Fatalf("clearTrackedMessages returned error: %v", err)
	}
	if len(failures) != 1 || failures[0].message.MessageID != "denied" {
		t.Fatalf("failures = %+v, want only the 403 message", failures)
	}
	// The poisoned ID is dropped from tracking anyway, the other feed kept.
	kept := storedMessages(t, bot)
	if len(kept) != 1 || kept[0].MessageID != "keep" || kept[0].Retailer != "fotex" {
		t.Fatalf("stored = %+v, want only the fotex entry", kept)
	}
}

func TestClearTrackedMessagesEmptyInputLeavesStoreAlone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	initial := []appconfig.PostedMessage{{ChannelID: "other", MessageID: "keep", Retailer: "fotex"}}
	bot := discordTestBot(t, server, initial)
	if failures, err := bot.clearTrackedMessages(nil); err != nil || len(failures) != 0 {
		t.Fatalf("failures = %+v, err = %v; want clean no-op", failures, err)
	}
	if kept := storedMessages(t, bot); len(kept) != 1 || kept[0].MessageID != "keep" {
		t.Fatalf("stored = %+v, want untouched", kept)
	}
}
