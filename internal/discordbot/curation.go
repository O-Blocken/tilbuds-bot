package discordbot

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/search"
	"spar-tilbuds-bot/internal/service"
	"spar-tilbuds-bot/internal/store"

	"github.com/bwmarrin/discordgo"
)

// storeWantRow builds a want row from user text. Text containing query
// syntax (quotes, exclusions, key:value filters) is stored as a raw
// query expression; plain text stays a legacy single search term, so
// existing keyword wants keep their meaning.
func storeWantRow(id, text string) store.WantRow {
	if search.HasQuerySyntax(text) {
		return store.WantRow{ID: id, Describe: text, Query: text}
	}
	return store.WantRow{ID: id, Describe: text, Includes: []string{text}}
}

var wantIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

func (b *DiscordBot) requireStore() bool {
	return b.db != nil
}

func (b *DiscordBot) handleWant(session *discordgo.Session, event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if len(data.Options) == 0 {
		b.replyEphemeral(session, event.Interaction, "Brug /want add, /want remove eller /want list.")
		return
	}
	if !b.requireStore() {
		b.replyEphemeral(session, event.Interaction, "Ønskedatabasen er ikke tilgængelig; ønsker styres via config.toml.")
		return
	}
	sub := data.Options[0]
	switch sub.Name {
	case "add":
		b.handleWantAdd(session, event, sub.Options)
	case "remove":
		b.handleWantRemove(session, event, sub.Options)
	case "list":
		b.handleWantList(session, event)
	default:
		b.replyEphemeral(session, event.Interaction, "Ukendt /want-underkommando.")
	}
}

func subcommandString(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, option := range options {
		if option.Name == name {
			return strings.TrimSpace(option.StringValue())
		}
	}
	return ""
}

func (b *DiscordBot) handleWantAdd(session *discordgo.Session, event *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	id := strings.ToLower(subcommandString(options, "id"))
	text := subcommandString(options, "tekst")
	if !wantIDPattern.MatchString(id) {
		b.replyEphemeral(session, event.Interaction, "ID skal være 1-40 tegn: små bogstaver, tal, - eller _.")
		return
	}
	if text == "" {
		b.replyEphemeral(session, event.Interaction, "Skriv en tekst for ønsket.")
		return
	}
	// Validate query expressions when the subscription changes, not
	// during scheduled scans.
	row := storeWantRow(id, text)
	if row.Query != "" {
		if _, err := match.WantQuery(match.Want{ID: id, Query: row.Query}, match.DefaultConcepts()); err != nil {
			if parseErr, ok := err.(*search.ParseError); ok {
				b.replyEphemeral(session, event.Interaction, parseErr.UserMessage())
				return
			}
			b.replyEphemeral(session, event.Interaction, b.userError("Ugyldig søgning", err))
			return
		}
	}
	err := b.db.UpsertWant(row)
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke gemme ønsket", err))
		return
	}
	b.replyEphemeral(session, event.Interaction, fmt.Sprintf("Gemte ønske %q. Det gælder fra næste kørsel.", id))
}

func (b *DiscordBot) handleWantRemove(session *discordgo.Session, event *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	id := strings.ToLower(subcommandString(options, "id"))
	deleted, err := b.db.DeleteWant(id)
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke slette ønsket", err))
		return
	}
	if !deleted {
		b.replyEphemeral(session, event.Interaction, fmt.Sprintf("Fandt intet ønske %q.", id))
		return
	}
	b.replyEphemeral(session, event.Interaction, fmt.Sprintf("Slettede ønske %q.", id))
}

func (b *DiscordBot) handleWantList(session *discordgo.Session, event *discordgo.InteractionCreate) {
	rows, err := b.db.ListWants()
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke læse ønsker", err))
		return
	}
	if len(rows) == 0 {
		b.replyEphemeral(session, event.Interaction, "Ingen ønsker i databasen.")
		return
	}
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("%d ønsker:\n", len(rows)))
	for _, row := range rows {
		builder.WriteString(fmt.Sprintf("\n• %s — %d søgeord (tærskel %.2f)", row.ID, len(row.Includes), row.Threshold))
		if row.Describe != "" {
			builder.WriteString(fmt.Sprintf("\n  %s", row.Describe))
		}
	}
	b.replyEphemeral(session, event.Interaction, builder.String())
}

func (b *DiscordBot) handleTop(session *discordgo.Session, event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	limit := 5
	if option := data.GetOption("limit"); option != nil {
		limit = clampInt(int(option.IntValue()), 1, 10)
	}
	retailer := retailerOption(data, service.RetailerSPAR)

	if err := b.deferEphemeral(session, event.Interaction); err != nil {
		b.logger.Error("failed to defer top response", slog.String("error", err.Error()))
		return
	}

	go func() {
		config, err := b.configStore.Load()
		if err != nil {
			b.followupEphemeral(session, event.Interaction, b.userError("Kunne ikke læse config", err))
			return
		}
		wants, err := b.loadWants(config)
		if err != nil {
			b.followupEphemeral(session, event.Interaction, b.userError("Kunne ikke læse ønsker", err))
			return
		}

		b.dealService.SetMatcher(match.ForName(config.Matcher))
		deals, err := b.dealService.TopDeals(b.ctx, retailer, wants, limit)
		if err != nil {
			b.followupEphemeral(session, event.Interaction, b.userError("Fejl under hentning af top", err))
			return
		}

		b.followupEphemeral(session, event.Interaction, buildTopContent(deals))
	}()
}

func buildTopContent(deals []match.ScoredDeal) string {
	if len(deals) == 0 {
		return "Ingen rangerede tilbud fundet."
	}
	var builder strings.Builder
	builder.WriteString("Ugens top:\n")
	for index, scored := range deals {
		deal := scored.Deal
		builder.WriteString(fmt.Sprintf("\n%d. %s", index+1, previewName(deal)))
		if deal.Price != nil {
			builder.WriteString(fmt.Sprintf(" - %.0f kr", *deal.Price))
		}
		builder.WriteString(fmt.Sprintf(" (%.2f)", scored.RankScore()))
		for _, badge := range scored.Badges {
			builder.WriteString(fmt.Sprintf("\n   ✅ %s", badge))
		}
	}
	return builder.String()
}

func (b *DiscordBot) handleStats(session *discordgo.Session, event *discordgo.InteractionCreate, _ discordgo.ApplicationCommandInteractionData) {
	config, err := b.configStore.Load()
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke læse config", err))
		return
	}
	wants, err := b.loadWants(config)
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke læse ønsker", err))
		return
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Matcher: %s", match.ForName(config.Matcher).Name()))
	builder.WriteString(fmt.Sprintf("\nØnsker: %d", len(wants)))
	for _, want := range wants {
		builder.WriteString(fmt.Sprintf("\n• %s — %d inkl., %d ekskl., tærskel %.2f",
			want.ID, len(want.Include), len(want.Exclude), want.EffectiveThreshold()))
	}
	if b.db != nil {
		stats, err := b.db.Stats()
		if err != nil {
			builder.WriteString(fmt.Sprintf("\nDatabasefejl: %s", err))
		} else {
			builder.WriteString(fmt.Sprintf("\nDatabase: %d ønsker, %d prisobservationer, %d prej-cache, %d feeds, %d koncepter, %d prisalarmer",
				stats.Wants, stats.Observations, stats.PrejCached, stats.Feeds, stats.Concepts, stats.Alerts))
		}
	} else {
		builder.WriteString("\nDatabase: ikke tilsluttet (kun TOML)")
	}
	b.replyEphemeral(session, event.Interaction, builder.String())
}
