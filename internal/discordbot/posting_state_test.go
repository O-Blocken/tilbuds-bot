package discordbot

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	appconfig "spar-tilbuds-bot/internal/config"
	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/service"

	"github.com/bwmarrin/discordgo"
	"tjekapi"
)

// fakeSession is a botSession that records calls instead of talking to Discord.
type fakeSession struct {
	mu sync.Mutex
	// failAt makes the Nth send (1-based) fail; 0 never fails.
	failAt   int
	sends    int
	sent     []string
	deleted  []string
	nextID   int
	channels map[string]*discordgo.Channel
}

func newFakeSession() *fakeSession {
	return &fakeSession{channels: map[string]*discordgo.Channel{}}
}

func (f *fakeSession) Channel(id string, _ ...discordgo.RequestOption) (*discordgo.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if channel, ok := f.channels[id]; ok {
		return channel, nil
	}
	return &discordgo.Channel{ID: id, Name: "deals", Type: discordgo.ChannelTypeGuildText}, nil
}

func (f *fakeSession) ChannelMessageSend(id, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	return f.send(id, content)
}

func (f *fakeSession) ChannelMessageSendComplex(id string, data *discordgo.MessageSend, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	content := data.Content
	if len(data.Embeds) > 0 {
		content = data.Embeds[0].Title
	}
	return f.send(id, content)
}

func (f *fakeSession) send(channelID, content string) (*discordgo.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends++
	if f.failAt > 0 && f.sends >= f.failAt {
		return nil, errors.New("discord unavailable")
	}
	f.nextID++
	f.sent = append(f.sent, fmt.Sprintf("%s:%s", channelID, content))
	return &discordgo.Message{ID: fmt.Sprintf("msg-%d", f.nextID), ChannelID: channelID}, nil
}

func (f *fakeSession) ChannelMessageDelete(channelID, messageID string, _ ...discordgo.RequestOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, channelID+"/"+messageID)
	return nil
}

func newPostingBot(t *testing.T, session botSession) (*DiscordBot, *appconfig.ConfigStore) {
	t.Helper()
	configStore := appconfig.NewConfigStore(filepath.Join(t.TempDir(), "config.toml"))
	return &DiscordBot{
		dealService: service.NewDealService(nil, nil, nil),
		configStore: configStore,
		logger:      discardLogger(),
		session:     session,
		ctx:         t.Context(),
	}, configStore
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// discardLogger swallows log output; captureLogger records it for assertions.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func captureLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buffer := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buffer, &slog.HandlerOptions{Level: slog.LevelDebug})), buffer
}

func testChannel() *discordgo.Channel {
	return &discordgo.Channel{ID: "100", Name: "deals", Type: discordgo.ChannelTypeGuildText}
}

func testFeed() feed {
	return feed{retailer: service.RetailerSPAR, channelID: 100, pingRoleID: 0}
}

func scoredDeals(count int) []match.ScoredDeal {
	price := 10.0
	deals := make([]match.ScoredDeal, 0, count)
	for i := 0; i < count; i++ {
		deals = append(deals, match.ScoredDeal{
			Deal: tjekapi.Deal{
				OfferName: fmt.Sprintf("Tilbud %d", i+1),
				CleanName: fmt.Sprintf("Tilbud %d", i+1),
				Price:     &price,
				OfferID:   fmt.Sprintf("offer-%d", i+1),
			},
			WantID: "test",
			Score:  1,
		})
	}
	return deals
}

// TestPublishFetchedEmptyLeavesPublicationUnposted is the regression test for a
// week being marked as posted even though nothing was sent.
func TestPublishFetchedEmptyLeavesPublicationUnposted(t *testing.T) {
	session := newFakeSession()
	bot, configStore := newPostingBot(t, session)

	posted, err := bot.publishFetched(testChannel(), testFeed(), "pub-1", appconfig.BotConfig{}, nil, false)
	if err != nil {
		t.Fatalf("publishFetched() error = %v", err)
	}
	if posted != 0 {
		t.Fatalf("posted = %d, want 0", posted)
	}
	if len(session.sent) != 0 {
		t.Fatalf("sent %d messages for an empty deal set", len(session.sent))
	}

	config, err := configStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.LastWeek != "" {
		t.Fatalf("LastWeek = %q, want empty: an empty run must not mark the week posted", config.LastWeek)
	}
	if len(config.PostedMessages) != 0 {
		t.Fatalf("tracked messages = %+v, want none", config.PostedMessages)
	}
}

// TestPublishFetchedSuccessMarksPublished covers the happy path: one message
// per deal, ids tracked, publication recorded.
func TestPublishFetchedSuccessMarksPublished(t *testing.T) {
	session := newFakeSession()
	bot, configStore := newPostingBot(t, session)

	posted, err := bot.publishFetched(testChannel(), testFeed(), "pub-1", appconfig.BotConfig{}, scoredDeals(3), false)
	if err != nil {
		t.Fatalf("publishFetched() error = %v", err)
	}
	if posted != 3 {
		t.Fatalf("posted = %d, want 3", posted)
	}
	if len(session.sent) != 3 {
		t.Fatalf("sent %d messages, want 3", len(session.sent))
	}

	config, err := configStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.LastWeek != "pub-1" {
		t.Fatalf("LastWeek = %q, want pub-1", config.LastWeek)
	}
	if len(config.PostedMessages) != 3 {
		t.Fatalf("tracked messages = %d, want 3", len(config.PostedMessages))
	}
	for _, message := range config.PostedMessages {
		if message.Retailer != service.RetailerSPAR {
			t.Errorf("tracked message retailer = %q, want spar", message.Retailer)
		}
	}
}

// TestPublishFetchedPartialFailureKeepsTracking covers the other half: messages
// that did reach Discord must stay tracked so cleanup can find them, while the
// publication stays unposted so the next check retries.
func TestPublishFetchedPartialFailureKeepsTracking(t *testing.T) {
	session := newFakeSession()
	session.failAt = 3 // first two sends succeed, third fails
	bot, configStore := newPostingBot(t, session)

	posted, err := bot.publishFetched(testChannel(), testFeed(), "pub-1", appconfig.BotConfig{}, scoredDeals(5), false)
	if err == nil {
		t.Fatal("publishFetched() error = nil, want the send failure")
	}
	if posted != 2 {
		t.Fatalf("posted = %d, want the 2 messages that reached Discord", posted)
	}

	config, err := configStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.LastWeek != "" {
		t.Fatalf("LastWeek = %q, want empty after a partial failure", config.LastWeek)
	}
	if len(config.PostedMessages) != 2 {
		t.Fatalf("tracked messages = %d, want 2 (partial posts must not leak)", len(config.PostedMessages))
	}
}

// TestPublishFetchedRefreshDeletesPreviousMessages covers the full-refresh
// cleanup path used by the scheduled run.
func TestPublishFetchedRefreshDeletesPreviousMessages(t *testing.T) {
	session := newFakeSession()
	bot, configStore := newPostingBot(t, session)

	previous := appconfig.BotConfig{PostedMessages: []appconfig.PostedMessage{
		{Retailer: service.RetailerSPAR, ChannelID: "100", MessageID: "old-1"},
		{Retailer: service.RetailerSPAR, ChannelID: "100", MessageID: "old-2"},
		{Retailer: service.RetailerFotex, ChannelID: "200", MessageID: "other-feed"},
	}}
	if err := configStore.Save(previous); err != nil {
		t.Fatal(err)
	}
	if _, err := bot.publishFetched(testChannel(), testFeed(), "pub-1", previous, scoredDeals(1), false); err != nil {
		t.Fatalf("publishFetched() error = %v", err)
	}

	if len(session.deleted) != 2 {
		t.Fatalf("deleted = %v, want only the two SPAR messages", session.deleted)
	}
	config, err := configStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range config.PostedMessages {
		if message.MessageID == "old-1" || message.MessageID == "old-2" {
			t.Errorf("stale message %q still tracked", message.MessageID)
		}
	}
	if len(config.PostedMessages) != 2 { // one remaining fotex entry + the new post
		t.Fatalf("tracked messages = %+v, want the fotex entry plus the new post", config.PostedMessages)
	}
}

// TestPublishFetchedIncrementalKeepsExistingMessages covers /send_deals: it
// appends new offers and must never delete what is already posted.
func TestPublishFetchedIncrementalKeepsExistingMessages(t *testing.T) {
	session := newFakeSession()
	bot, configStore := newPostingBot(t, session)

	previous := appconfig.BotConfig{
		LastWeek: "pub-1",
		PostedMessages: []appconfig.PostedMessage{
			{Retailer: service.RetailerSPAR, ChannelID: "100", MessageID: "already-posted"},
		},
	}
	if err := configStore.Save(previous); err != nil {
		t.Fatal(err)
	}
	if _, err := bot.publishFetched(testChannel(), testFeed(), "pub-1", previous, scoredDeals(1), true); err != nil {
		t.Fatalf("publishFetched() error = %v", err)
	}

	if len(session.deleted) != 0 {
		t.Fatalf("deleted = %v, want no deletions on an incremental send", session.deleted)
	}
	config, err := configStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.PostedMessages) != 2 {
		t.Fatalf("tracked messages = %+v, want the existing entry plus the new post", config.PostedMessages)
	}
}

// TestResolvePublishChannelRejectsMissingChannel covers the guard that keeps a
// feed from posting into channel 0.
func TestResolvePublishChannelRejectsMissingChannel(t *testing.T) {
	bot, _ := newPostingBot(t, newFakeSession())
	if _, err := bot.resolvePublishChannel(feed{retailer: service.RetailerSPAR}, ""); err == nil {
		t.Fatal("resolvePublishChannel() error = nil, want a missing-channel error")
	}
}

// TestResolvePublishChannelRejectsNonTextChannel covers the voice/category case.
func TestResolvePublishChannelRejectsNonTextChannel(t *testing.T) {
	session := newFakeSession()
	session.channels["voice"] = &discordgo.Channel{ID: "voice", Type: discordgo.ChannelTypeGuildVoice}
	bot, _ := newPostingBot(t, session)
	if _, err := bot.resolvePublishChannel(testFeed(), "voice"); err == nil {
		t.Fatal("resolvePublishChannel() error = nil, want a non-text-channel error")
	}
}

// TestPublishFeedRejectsConcurrentRun covers the overlap guard: while one run
// holds the publish lock, a second one is refused instead of double-posting.
func TestPublishFeedRejectsConcurrentRun(t *testing.T) {
	bot, _ := newPostingBot(t, newFakeSession())
	bot.publishMu.Lock()
	defer bot.publishMu.Unlock()

	if _, err := bot.publishFeed(testFeed(), nil, "pub-1", "", false); !errors.Is(err, errPublishBusy) {
		t.Fatalf("publishFeed() error = %v, want errPublishBusy", err)
	}
}
