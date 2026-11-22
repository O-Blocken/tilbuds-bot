package discordbot

import (
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	appconfig "spar-tilbuds-bot/internal/config"
	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/service"
	"spar-tilbuds-bot/internal/store"

	"github.com/bwmarrin/discordgo"
	"tjekapi"
)

func (b *DiscordBot) startAutoPosting() {
	if !b.autoStarted.CompareAndSwap(false, true) {
		return
	}

	go func() {
		for {
			delay := b.nextAutoPostDelay()
			timer := time.NewTimer(delay)
			select {
			case <-b.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				b.checkAndUpdateDeals()
			}
		}
	}()
}

func (b *DiscordBot) nextAutoPostDelay() time.Duration {
	config, err := b.configStore.Load()
	cronLine := appconfig.DefaultCheckCron
	if err != nil {
		b.logger.Error("failed to load config for cron schedule", slog.String("error", err.Error()))
	} else {
		cronLine = config.CheckCron
	}

	delay, err := nextCronDelay(cronLine, time.Now())
	if err != nil {
		b.logger.Error(
			"invalid cron schedule, falling back to default",
			slog.String("cron", cronLine),
			slog.String("default", appconfig.DefaultCheckCron),
			slog.String("error", err.Error()),
		)
		cronLine = appconfig.DefaultCheckCron
		delay, err = nextCronDelay(appconfig.DefaultCheckCron, time.Now())
		if err != nil {
			return 30 * time.Minute
		}
	}

	b.logger.Info("next automatic deal check scheduled", slog.String("cron", cronLine), slog.Duration("delay", delay))
	return delay
}

// feed describes one retailer's posting target.
type feed struct {
	retailer   string
	channelID  int64
	pingRoleID int64
}

// feedsFromConfig returns the configured feeds: SPAR always, Føtex and
// Lidl only when their channels are set. Auto-posting each extra feed
// additionally needs SQLite for per-feed publication tracking (warned
// once per check).
func feedsFromConfig(config appconfig.BotConfig) []feed {
	feeds := []feed{{retailer: service.RetailerSPAR, channelID: config.ChannelID, pingRoleID: config.PingRoleID}}
	if config.FotexChannelID > 0 {
		feeds = append(feeds, feed{retailer: service.RetailerFotex, channelID: config.FotexChannelID, pingRoleID: config.FotexPingRoleID})
	}
	if config.LidlChannelID > 0 {
		feeds = append(feeds, feed{retailer: service.RetailerLidl, channelID: config.LidlChannelID, pingRoleID: config.LidlPingRoleID})
	}
	return feeds
}

func retailerDisplay(retailer string) string {
	switch retailer {
	case service.RetailerFotex:
		return "Føtex"
	case service.RetailerLidl:
		return "Lidl"
	default:
		return "SPAR"
	}
}

func (b *DiscordBot) checkAndUpdateDeals() {
	b.logger.Info("checking if deals should be posted")

	config, err := b.configStore.Load()
	if err != nil {
		b.logger.Error("failed to load config", slog.String("error", err.Error()))
		return
	}

	if !b.notificationTimeReached(config.NotificationTime) {
		return
	}

	wants, err := b.loadWants(config)
	if err != nil {
		b.logger.Error("failed to load wants", slog.String("error", err.Error()))
		return
	}
	b.dealService.SetMatcher(match.ForName(config.Matcher))

	for _, feed := range feedsFromConfig(config) {
		b.checkAndUpdateFeed(feed, wants)
	}
}

func (b *DiscordBot) checkAndUpdateFeed(feed feed, wants []match.Want) {
	logger := b.logger.With(slog.String("retailer", feed.retailer))

	publicationID, err := b.dealService.GetRetailerWeekContext(b.ctx, feed.retailer)
	if err != nil {
		logger.Error("failed to read current publication", slog.String("error", err.Error()))
		return
	}

	if b.lastPostedPublication(feed.retailer) == publicationID {
		logger.Info("publication already posted", slog.String("publication_id", publicationID))
		return
	}

	if feed.channelID <= 0 {
		logger.Warn("no deals channel configured")
		return
	}

	channelID := strconv.FormatInt(feed.channelID, 10)
	channel, err := b.session.Channel(channelID)
	if err != nil || !isTextChannel(channel) {
		logger.Warn("configured channel not found or invalid", slog.String("channel_id", channelID))
		return
	}

	posted, err := b.publishFeed(feed, wants, publicationID, channel.ID, false)
	if err != nil {
		logger.Error("failed to post deals", slog.String("error", err.Error()))
		return
	}

	// Fire price alerts against the fresh publication (once each).
	if b.db != nil {
		if rows, err := b.db.ListPriceAlerts(); err == nil {
			b.evaluateAlerts(feed, rows, true)
		}
	}

	logger.Info("successfully posted deals", slog.Int("deals", posted), slog.String("publication_id", publicationID))

	// Once a week is often enough to keep the history tables bounded.
	b.pruneHistory()
}

// pruneHistory drops rows past their retention window and posted-deal keys from
// publications the feeds have moved on from. Best effort: a failure only logs,
// because stale rows must never block posting.
func (b *DiscordBot) pruneHistory() {
	if b.db == nil {
		return
	}
	stats, err := b.db.PruneHistory(time.Now())
	if err != nil {
		b.logger.Warn("failed to prune history", slog.String("error", err.Error()))
		return
	}
	if stats.Total() > 0 {
		b.logger.Info("pruned old rows",
			slog.Int64("price_observations", stats.Observations),
			slog.Int64("prej_cache", stats.PrejCached),
			slog.Int64("posted_deals", stats.PostedDeals))
	}
}

// publishFeed fetches the feed's deals, posts one message per deal and
// persists publication markers plus message ids.
//
// Two posting modes share this path:
//
//   - refresh (onlyNew=false, the scheduled run): previous messages of the feed
//     are deleted, then the full current set is posted, so a changed catalogue
//     never leaves stale offers behind.
//   - incremental (onlyNew=true, /send_deals): only offers not yet posted for
//     this publication are appended and nothing is deleted.
//
// The publication is only recorded once a run completes. An empty result or a
// mid-way failure therefore never marks the week as done, so the next scheduled
// check retries instead of silently skipping the publication.
func (b *DiscordBot) publishFeed(feed feed, wants []match.Want, publicationID, channelOverride string, onlyNew bool) (int, error) {
	// One publish at a time: the scheduled run and a manual /send_deals must not
	// interleave, or both would clear and post the same publication.
	if !b.publishMu.TryLock() {
		return 0, errPublishBusy
	}
	defer b.publishMu.Unlock()

	channel, err := b.resolvePublishChannel(feed, channelOverride)
	if err != nil {
		return 0, err
	}
	config, err := b.configStore.Load()
	if err != nil {
		return 0, err
	}

	// Fetch before touching anything: a failed fetch must never wipe the
	// channel, and an empty result leaves posted deals in place.
	deals, err := b.dealService.FetchAndProcessRetailerDeals(b.ctx, feed.retailer, wants)
	if err != nil {
		return 0, err
	}
	if onlyNew {
		deals = b.filterUnseenDeals(feed.retailer, publicationID, deals)
	}
	return b.publishFetched(channel, feed, publicationID, config, deals, onlyNew)
}

// resolvePublishChannel returns the channel to post to: the explicit override,
// else the feed's configured channel.
func (b *DiscordBot) resolvePublishChannel(feed feed, channelOverride string) (*discordgo.Channel, error) {
	targetID := channelOverride
	if targetID == "" {
		config, err := b.configStore.Load()
		if err != nil {
			return nil, err
		}
		channelID := feed.channelID
		if channelID <= 0 {
			// Config may have changed since the feed list was built.
			for _, candidate := range feedsFromConfig(config) {
				if candidate.retailer == feed.retailer {
					channelID = candidate.channelID
				}
			}
		}
		if channelID <= 0 {
			return nil, fmt.Errorf("no deals channel configured")
		}
		targetID = strconv.FormatInt(channelID, 10)
	}
	channel, err := b.session.Channel(targetID)
	if err != nil || !isTextChannel(channel) {
		return nil, fmt.Errorf("configured channel not found or invalid")
	}
	return channel, nil
}

// publishFetched posts an already-fetched deal set and persists its state. It
// is separated from publishFeed so the state machine (empty set, partial
// failure, only-new) is testable without the Tjek/PDF pipeline.
func (b *DiscordBot) publishFetched(channel *discordgo.Channel, feed feed, publicationID string, config appconfig.BotConfig, deals []match.ScoredDeal, onlyNew bool) (int, error) {
	logger := b.logger.With(slog.String("retailer", feed.retailer))
	if len(deals) == 0 {
		logger.Info("no deals to post; leaving channel and publication marker untouched")
		return 0, nil
	}

	if !onlyNew {
		tracked := appconfig.FilterPostedMessages(config.PostedMessages, feed.retailer)
		logger.Info("posting deals", slog.String("channel", channel.Name), slog.Int("count", len(deals)))
		// Cleanup is best-effort: a message the bot can no longer delete
		// (archived thread, lost permissions, …) must never block posting.
		if failures, err := b.clearTrackedMessages(tracked); err != nil {
			logger.Warn("failed to persist cleaned tracking list", slog.String("error", err.Error()))
		} else if len(failures) > 0 {
			logger.Warn("some tracked messages could not be deleted; continuing with posting",
				slog.Int("failed", len(failures)), slog.Int("attempted", len(tracked)))
			for _, failure := range failures {
				logger.Warn("tracked message cleanup failed",
					slog.String("channel_id", failure.message.ChannelID),
					slog.String("message_id", failure.message.MessageID),
					slog.String("error", failure.err.Error()))
			}
		}
	}

	postedMessages, postErr := b.postDeals(channel.ID, feed, deals)
	// Persist whatever reached Discord even when posting aborted midway:
	// an untracked message can never be cleaned up later. The publication
	// marker itself is only written for a completed run.
	persistErr := b.persistPublished(feed, publicationID, deals, postedMessages, postErr == nil)
	if postErr != nil {
		// Report what actually reached Discord, not what was intended.
		return len(postedMessages), postErr
	}
	if persistErr != nil {
		return len(deals), persistErr
	}
	return len(deals), nil
}

// persistPublished stores the tracked message ids for this run and, when the
// run completed, the publication markers that stop it from posting twice.
func (b *DiscordBot) persistPublished(feed feed, publicationID string, deals []match.ScoredDeal, posted []appconfig.PostedMessage, completed bool) error {
	if len(posted) > 0 || completed {
		if _, err := b.configStore.Update(func(config *appconfig.BotConfig) {
			if completed {
				switch feed.retailer {
				case service.RetailerFotex:
					config.FotexLastWeek = publicationID
				case service.RetailerLidl:
					config.LidlLastWeek = publicationID
				default:
					config.LastWeek = publicationID
				}
			}
			config.PostedMessages = append(config.PostedMessages, posted...)
		}); err != nil {
			return err
		}
	}
	if !completed {
		return nil
	}
	if err := b.rememberFeedPublication(feed, publicationID); err != nil {
		return err
	}
	b.rememberPostedDeals(feed.retailer, publicationID, deals)
	return nil
}

// lastPostedPublication returns the last posted publication for a feed:
// SQLite feed state wins, TOML is the fallback (and the only source
// without a database).
func (b *DiscordBot) lastPostedPublication(retailer string) string {
	if b.db != nil {
		if feed, err := b.db.GetFeed(retailer); err == nil && feed.LastPublicationID != "" {
			return feed.LastPublicationID
		}
	}
	config, err := b.configStore.Load()
	if err != nil {
		return ""
	}
	switch retailer {
	case service.RetailerFotex:
		return strings.TrimSpace(config.FotexLastWeek)
	case service.RetailerLidl:
		return strings.TrimSpace(config.LidlLastWeek)
	default:
		return strings.TrimSpace(config.LastWeek)
	}
}

// rememberFeedPublication persists per-feed publication + channel state.
func (b *DiscordBot) rememberFeedPublication(feed feed, publicationID string) error {
	if b.db == nil {
		return nil
	}
	return b.db.PutFeed(store.FeedState{Retailer: feed.retailer, ChannelID: feed.channelID, LastPublicationID: publicationID})
}

// filterUnseenDeals drops deals already posted for this publication
// (manual re-sends only post what is new). Without a database everything
// counts as unseen, preserving legacy behavior.
func (b *DiscordBot) filterUnseenDeals(retailer, publicationID string, deals []match.ScoredDeal) []match.ScoredDeal {
	if b.db == nil {
		return deals
	}
	posted, err := b.db.PostedKeys(retailer, publicationID)
	if err != nil || len(posted) == 0 {
		return deals
	}
	fresh := make([]match.ScoredDeal, 0, len(deals))
	for _, scored := range deals {
		if !posted[dealKey(scored.Deal)] {
			fresh = append(fresh, scored)
		}
	}
	return fresh
}

// rememberPostedDeals records posted offer keys for diffing later runs.
func (b *DiscordBot) rememberPostedDeals(retailer, publicationID string, deals []match.ScoredDeal) {
	if b.db == nil {
		return
	}
	keys := make([]string, 0, len(deals))
	for _, scored := range deals {
		keys = append(keys, dealKey(scored.Deal))
	}
	if err := b.db.AddPostedDeals(retailer, publicationID, keys); err != nil {
		b.logger.Warn("failed to record posted deals", slog.String("error", err.Error()))
	}
}

func dealKey(deal tjekapi.Deal) string {
	if deal.OfferID != "" {
		return deal.OfferID
	}
	return deal.OfferName
}

func (b *DiscordBot) notificationTimeReached(value string) bool {
	reached, normalized, err := notificationTimeReached(value, time.Now())
	if err != nil {
		b.logger.Error(
			"invalid notification_time, falling back to default",
			slog.String("notification_time", value),
			slog.String("default", appconfig.DefaultNotificationTime),
			slog.String("error", err.Error()),
		)
		reached, normalized, err = notificationTimeReached(appconfig.DefaultNotificationTime, time.Now())
		if err != nil {
			return true
		}
	}
	if !reached {
		b.logger.Info("notification time has not been reached yet", slog.String("notification_time", normalized))
	}
	return reached
}

// postDeals posts the classic message flood: an optional role ping followed
// by one message per deal. Everything is retailer-tagged for precise cleanup.
// Empty deal lists post nothing and track nothing.
func (b *DiscordBot) postDeals(channelID string, feed feed, input []match.ScoredDeal) ([]appconfig.PostedMessage, error) {
	postedMessages := make([]appconfig.PostedMessage, 0, len(input)+1)
	if len(input) == 0 {
		return postedMessages, nil
	}

	if shouldPingRole(feed.pingRoleID, len(input)) {
		sent, err := b.session.ChannelMessageSend(channelID, fmt.Sprintf("<@&%d>", feed.pingRoleID))
		if err != nil {
			return postedMessages, err
		}
		postedMessages = append(postedMessages, tagRetailer(trackedMessage(sent), feed.retailer))
	}

	for _, scored := range input {
		sent, err := b.sendDealMessage(channelID, scored)
		if err != nil {
			return postedMessages, err
		}
		postedMessages = append(postedMessages, tagRetailer(trackedMessage(sent), feed.retailer))
	}

	return postedMessages, nil
}

func tagRetailer(message appconfig.PostedMessage, retailer string) appconfig.PostedMessage {
	message.Retailer = retailer
	return message
}

// sendDealMessage posts one scored deal embed (+ image) to a channel.
func (b *DiscordBot) sendDealMessage(channelID string, scored match.ScoredDeal) (*discordgo.Message, error) {
	deal := scored.Deal
	embed := buildDealEmbed(scored)
	for _, badge := range scored.Badges {
		embed.Description += "\n✅ " + badge
	}
	if hasLowestEverBadge(scored.Badges) {
		embed.Title = "⭐ " + embed.Title
	}
	message := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}}

	var file *os.File
	var err error
	if deal.ImagePath != "" && fileExists(deal.ImagePath) {
		file, err = os.Open(deal.ImagePath)
		if err != nil {
			return nil, err
		}
		message.Files = []*discordgo.File{
			{
				Name:        filepath.Base(deal.ImagePath),
				ContentType: "image/png",
				Reader:      file,
			},
		}
	}

	sent, sendErr := b.session.ChannelMessageSendComplex(channelID, message)
	if file != nil {
		closeErr := file.Close()
		if sendErr == nil {
			sendErr = closeErr
		}
	}
	if sendErr != nil {
		return nil, sendErr
	}
	return sent, nil
}

// hasLowestEverBadge reports the spotlight badge: cheapest in 30 days.
func hasLowestEverBadge(badges []string) bool {
	for _, badge := range badges {
		if badge == "billigste i 30 dage" {
			return true
		}
	}
	return false
}

func shouldPingRole(pingRoleID int64, dealCount int) bool {
	return pingRoleID > 0 && dealCount > 0
}

func buildDealEmbed(scored match.ScoredDeal) *discordgo.MessageEmbed {
	deal := scored.Deal
	title := strings.TrimSpace(deal.CleanName)
	if title == "" {
		title = strings.TrimSpace(deal.OfferName)
	}
	if title == "" {
		title = "Unknown"
	}

	price := 0.0
	if deal.Price != nil {
		price = *deal.Price
	}

	description := fmt.Sprintf("Pris: %.0f kr", price)
	if deal.UnitPrice != nil && *deal.UnitPrice > 0 {
		description += fmt.Sprintf("\nPris for 1: %s kr", formatUnitPrice(*deal.UnitPrice))
	}
	if scored.UnitLabel != "" {
		description += "\nEnhedspris: " + scored.UnitLabel
	}
	if deal.IsAppPrice {
		description += "\n\n**⚠️ APP PRIS!**"
	}

	embed := &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
	}
	if deal.ImagePath != "" && fileExists(deal.ImagePath) {
		embed.Image = &discordgo.MessageEmbedImage{URL: "attachment://" + filepath.Base(deal.ImagePath)}
	}
	return embed
}

// formatUnitPrice renders a price with the Danish decimal comma, matching the
// unit-price label ("Enhedspris: 12,50 kr/kg").
func formatUnitPrice(price float64) string {
	if math.Abs(price-math.Round(price)) < 0.005 {
		return fmt.Sprintf("%.0f", price)
	}
	return strings.ReplaceAll(fmt.Sprintf("%.2f", price), ".", ",")
}
