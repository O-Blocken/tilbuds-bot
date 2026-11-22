package discordbot

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/search"
	"spar-tilbuds-bot/internal/service"
	"spar-tilbuds-bot/internal/store"

	"github.com/bwmarrin/discordgo"
)

// handlePrisalarm is the admin command for price alerts: creating alerts
// that fire when a product reaches a target price — or its lowest price
// ever seen in the own-observations history.
func (b *DiscordBot) handlePrisalarm(session *discordgo.Session, event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if len(data.Options) == 0 {
		b.replyEphemeral(session, event.Interaction, "Brug /prisalarm add, /prisalarm list, /prisalarm remove eller /prisalarm check.")
		return
	}
	sub := data.Options[0]
	switch sub.Name {
	case "add":
		b.handlePrisalarmAdd(session, event, sub.Options)
	case "list":
		b.handlePrisalarmList(session, event)
	case "remove":
		b.handlePrisalarmRemove(session, event, sub.Options)
	case "check":
		b.handlePrisalarmCheck(session, event)
	default:
		b.replyEphemeral(session, event.Interaction, "Ukendt /prisalarm-underkommando.")
	}
}

func (b *DiscordBot) alertsAvailable(session *discordgo.Session, interaction *discordgo.Interaction) bool {
	if b.db == nil {
		b.replyEphemeral(session, interaction, "Prisalarmer kræver databasen; kør uden SQLite er de ikke tilgængelige.")
		return false
	}
	return true
}

// handlePrisalarmAdd stores one alert. The product text uses the query
// language and is validated immediately so a typo is reported now, not
// during a scheduled scan.
func (b *DiscordBot) handlePrisalarmAdd(session *discordgo.Session, event *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if !b.alertsAvailable(session, event.Interaction) {
		return
	}
	query := strings.TrimSpace(subcommandString(options, "produkt"))
	if query == "" {
		b.replyEphemeral(session, event.Interaction, "Angiv et produktnavn eller en søgning.")
		return
	}
	if _, err := match.WantQuery(match.Want{ID: "alarm", Query: query}, match.DefaultConcepts()); err != nil {
		b.replyEphemeral(session, event.Interaction, userFacingSearchError(err))
		return
	}

	row := store.PriceAlertRow{Query: query}
	if option := dataOption(options, "pris"); option != nil {
		target := option.FloatValue()
		row.TargetPrice = &target
	}
	if option := dataOption(options, "butik"); option != nil {
		row.Retailer = strings.TrimSpace(option.StringValue())
	}
	if option := dataOption(options, "kanal"); option != nil {
		// Resolve and validate the channel like every other command: an alert
		// pointing at a voice channel or a category could never be delivered.
		channel := option.ChannelValue(session)
		if !isTextChannel(channel) {
			b.replyEphemeral(session, event.Interaction, "Vælg en tekstkanal til alarmen.")
			return
		}
		if channelID, err := strconv.ParseInt(channel.ID, 10, 64); err == nil {
			row.ChannelID = channelID
		}
	}
	if row.ChannelID == 0 {
		if channelID, err := strconv.ParseInt(event.ChannelID, 10, 64); err == nil {
			row.ChannelID = channelID
		}
	}
	if event.User != nil {
		row.UserID = event.User.ID
	}

	id, err := b.db.AddPriceAlert(row)
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke gemme alarmen", err))
		return
	}
	targetLabel := "laveste pris nogensinde set"
	if row.TargetPrice != nil {
		targetLabel = fmt.Sprintf("målpris %s kr", formatAlarmPrice(*row.TargetPrice))
	}
	b.replyEphemeral(session, event.Interaction,
		fmt.Sprintf("Gemte prisalarm #%d på %q (%s). Tjekkes ved hvert nye tilbud.", id, query, targetLabel))
}

// dataOption returns a named option from a subcommand's option list.
func dataOption(options []*discordgo.ApplicationCommandInteractionDataOption, name string) *discordgo.ApplicationCommandInteractionDataOption {
	for _, option := range options {
		if option.Name == name {
			return option
		}
	}
	return nil
}

// userFacingSearchError maps engine errors to the Danish user message
// where available. Only query validation errors reach this function, so the
// message is safe to show as-is.
func userFacingSearchError(err error) string {
	var parseErr *search.ParseError
	if errors.As(err, &parseErr) {
		return parseErr.UserMessage()
	}
	return fmt.Sprintf("Ugyldig søgning: %s", err)
}

func formatAlarmPrice(value float64) string {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	return strings.ReplaceAll(text, ".", ",")
}

// handlePrisalarmList shows all alerts with their ids and targets.
func (b *DiscordBot) handlePrisalarmList(session *discordgo.Session, event *discordgo.InteractionCreate) {
	if !b.alertsAvailable(session, event.Interaction) {
		return
	}
	rows, err := b.db.ListPriceAlerts()
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke læse alarmer", err))
		return
	}
	if len(rows) == 0 {
		b.replyEphemeral(session, event.Interaction, "Ingen prisalarmer oprettet endnu.")
		return
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "%d prisalarmer:\n", len(rows))
	for _, row := range rows {
		target := "laveste pris nogensinde"
		if row.TargetPrice != nil {
			target = "mål " + formatAlarmPrice(*row.TargetPrice) + " kr"
		}
		butik := "alle butikker"
		if row.Retailer != "" {
			butik = row.Retailer
		}
		status := "aktiv"
		if !row.Active {
			status = "på pause"
		}
		fmt.Fprintf(&builder, "\n#%d %q — %s, %s, %s", row.ID, row.Query, target, butik, status)
	}
	b.replyEphemeral(session, event.Interaction, builder.String())
}

// handlePrisalarmRemove deletes one alert by id.
func (b *DiscordBot) handlePrisalarmRemove(session *discordgo.Session, event *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if !b.alertsAvailable(session, event.Interaction) {
		return
	}
	option := dataOption(options, "id")
	if option == nil {
		b.replyEphemeral(session, event.Interaction, "Angiv alarmens id.")
		return
	}
	id := int64(option.IntValue())
	removed, err := b.db.DeletePriceAlert(id)
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke slette alarmen", err))
		return
	}
	if !removed {
		b.replyEphemeral(session, event.Interaction, fmt.Sprintf("Fandt ingen prisalarm #%d.", id))
		return
	}
	b.replyEphemeral(session, event.Interaction, fmt.Sprintf("Slettede prisalarm #%d.", id))
}

// handlePrisalarmCheck runs one evaluation immediately and reports the
// outcome as an ephemeral summary (no channel ping).
func (b *DiscordBot) handlePrisalarmCheck(session *discordgo.Session, event *discordgo.InteractionCreate) {
	if !b.alertsAvailable(session, event.Interaction) {
		return
	}
	if err := b.deferEphemeral(session, event.Interaction); err != nil {
		b.logger.Warn("failed to defer prisalarm check", slog.String("error", err.Error()))
		return
	}
	go func() {
		rows, err := b.db.ListPriceAlerts()
		if err != nil {
			b.followupEphemeral(session, event.Interaction, b.userError("Kunne ikke læse alarmer", err))
			return
		}
		config, err := b.configStore.Load()
		if err != nil {
			b.followupEphemeral(session, event.Interaction, b.userError("Kunne ikke læse config", err))
			return
		}
		feeds := feedsFromConfig(config)
		if len(feeds) == 0 {
			b.followupEphemeral(session, event.Interaction, "Ingen tilbudskanal konfigureret.")
			return
		}
		total := 0
		lines := []string{}
		for _, feed := range feeds {
			hits := b.evaluateAlerts(feed, rows, false)
			for _, hit := range hits {
				lines = append(lines, fmt.Sprintf("🔔 %s (%s) — %s kr", hit.query, feed.retailer, formatAlarmPrice(hit.price)))
				total++
			}
		}
		if total == 0 {
			b.followupEphemeral(session, event.Interaction, "Ingen alarmer matcher lige nu.")
			return
		}
		b.followupEphemeral(session, event.Interaction,
			fmt.Sprintf("%d alarmer matcher lige nu:\n%s", total, strings.Join(lines, "\n")))
	}()
}

// alertHit is one fired evaluation result.
type alertHit struct {
	alertID   int64
	query     string
	retailer  string
	price     float64
	channelID int64
	userID    string
	name      string
	reason    string
	target    *float64
}

// evaluateAlerts checks every active alert against the feed's current
// publication and returns the hits. With fire enabled, matching alerts
// are pinged into their channel and marked as fired for this publication
// so a weekly feed never spams twice.
func (b *DiscordBot) evaluateAlerts(feed feed, rows []store.PriceAlertRow, fire bool) []alertHit {
	if b.db == nil {
		return nil
	}
	publicationID, err := b.dealService.GetRetailerWeekContext(b.ctx, feed.retailer)
	if err != nil {
		b.logger.Warn("prisalarm: publication lookup failed",
			slog.String("retailer", feed.retailer), slog.String("error", err.Error()))
		return nil
	}

	active := make([]store.PriceAlertRow, 0, len(rows))
	for _, row := range rows {
		if !row.Active {
			continue
		}
		if row.Retailer != "" && row.Retailer != feed.retailer {
			continue
		}
		if fire && row.FiredPublication == publicationID {
			continue // already pinged for this week's feed
		}
		active = append(active, row)
	}
	if len(active) == 0 {
		return nil
	}

	queries := make([]string, len(active))
	for i, row := range active {
		queries[i] = row.Query
	}
	perQuery, err := b.dealService.MatchAlertQueries(b.ctx, feed.retailer, queries, 5)
	if err != nil {
		b.logger.Warn("prisalarm: matching failed",
			slog.String("retailer", feed.retailer), slog.String("error", err.Error()))
		return nil
	}

	hits := make([]alertHit, 0, 3)
	for i, row := range active {
		hit, ok := b.bestAlertHit(row, feed.retailer, publicationID, perQuery[i])
		if !ok {
			continue
		}
		hits = append(hits, hit)
	}
	if !fire {
		return hits
	}
	b.fireAlerts(feed, publicationID, hits)
	return hits
}

// bestAlertHit picks the cheapest match for one alert and decides whether
// the alert fires: target mode fires at or below the target price,
// all-time-low mode fires when the price undercuts the cheapest price
// seen in other publications of the same retailer.
func (b *DiscordBot) bestAlertHit(row store.PriceAlertRow, retailer, publicationID string, matches []service.AlertHit) (alertHit, bool) {
	if len(matches) == 0 {
		return alertHit{}, false
	}
	best := matches[0]
	for _, candidate := range matches[1:] {
		if priceOf(candidate.Scored) < priceOf(best.Scored) {
			best = candidate
		}
	}
	price := priceOf(best.Scored)
	if price <= 0 {
		return alertHit{}, false
	}

	fires := false
	if row.TargetPrice != nil {
		fires = price <= *row.TargetPrice
	} else {
		key := match.NormKey(best.Scored.Deal.CleanName, 0, 0)
		cheapest, _, ok, err := b.db.CheapestOutside(retailer, key, publicationID)
		if err == nil && ok && price < cheapest {
			fires = true
		}
	}
	if !fires {
		return alertHit{}, false
	}
	name := best.Scored.Deal.CleanName
	if name == "" {
		name = best.Scored.Deal.OfferName
	}
	return alertHit{
		alertID:   row.ID,
		query:     row.Query,
		retailer:  row.Retailer,
		price:     price,
		channelID: row.ChannelID,
		userID:    row.UserID,
		name:      name,
		reason:    best.Scored.Reason,
		target:    row.TargetPrice,
	}, true
}

func priceOf(scored match.ScoredDeal) float64 {
	if scored.Deal.Price == nil {
		return 0
	}
	return *scored.Deal.Price
}

// fireAlerts posts one message per hit and remembers the publication so
// the same weekly feed never pings the same alert twice.
func (b *DiscordBot) fireAlerts(feed feed, publicationID string, hits []alertHit) {
	for _, hit := range hits {
		channelID := hit.channelID
		if channelID <= 0 {
			channelID = feed.channelID
		}
		if channelID <= 0 {
			b.logger.Warn("prisalarm: no channel for alert", slog.Int64("alert_id", hit.alertID))
			continue
		}
		var content strings.Builder
		fmt.Fprintf(&content, "🔔 **Prisalarm** %s\n%s — **%s kr**", hit.query, hit.name, formatAlarmPrice(hit.price))
		if hit.target != nil {
			fmt.Fprintf(&content, " (mål %s kr)", formatAlarmPrice(*hit.target))
		} else {
			content.WriteString(" (laveste pris nogensinde set!)")
		}
		if hit.userID != "" {
			content.WriteString("\n<@" + hit.userID + ">")
		}
		if _, err := b.session.ChannelMessageSend(strconv.FormatInt(channelID, 10), content.String()); err != nil {
			b.logger.Warn("prisalarm: failed to send alert",
				slog.Int64("alert_id", hit.alertID), slog.String("error", err.Error()))
			continue
		}
		if err := b.db.MarkPriceAlertFired(hit.alertID, publicationID); err != nil {
			b.logger.Warn("prisalarm: failed to mark alert fired",
				slog.Int64("alert_id", hit.alertID), slog.String("error", err.Error()))
		}
	}
}
