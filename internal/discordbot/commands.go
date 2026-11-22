package discordbot

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/search"
	"spar-tilbuds-bot/internal/service"

	appconfig "spar-tilbuds-bot/internal/config"

	"github.com/bwmarrin/discordgo"
)

func (b *DiscordBot) registerCommands(session *discordgo.Session, event *discordgo.Ready) error {
	appID := ""
	if event.Application != nil {
		appID = event.Application.ID
	}
	if appID == "" && event.User != nil {
		appID = event.User.ID
	}
	if appID == "" {
		return fmt.Errorf("missing application ID")
	}

	adminOnly := int64(discordgo.PermissionAdministrator)
	minLimit := 1.0
	maxPreview := 25.0
	commands := []*discordgo.ApplicationCommand{
		{
			Name:                     "set_deals_channel",
			Description:              "Vælg hvilken kanal der skal modtage tilbud.",
			DefaultMemberPermissions: &adminOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:         discordgo.ApplicationCommandOptionChannel,
					Name:         "channel",
					Description:  "Kanal hvor tilbuddene postes",
					Required:     true,
					ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "butik",
					Description: "Hvilken butik (standard: spar)",
					Required:    false,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "SPAR", Value: "spar"},
						{Name: "Føtex", Value: "fotex"},
						{Name: "Lidl", Value: "lidl"},
					},
				},
			},
		},
		{
			Name:                     "send_deals",
			Description:              "Hent og post udvalgte tilbud",
			DefaultMemberPermissions: &adminOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:         discordgo.ApplicationCommandOptionChannel,
					Name:         "channel",
					Description:  "Valgfri kanal (ellers bruges de gemte)",
					Required:     false,
					ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "butik",
					Description: "Hvilken butik (standard: alle)",
					Required:    false,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "Alle", Value: "alle"},
						{Name: "SPAR", Value: "spar"},
						{Name: "Føtex", Value: "fotex"},
						{Name: "Lidl", Value: "lidl"},
					},
				},
			},
		},
		{
			Name:                     "clear_deals",
			Description:              "Slet bottens tidligere postede tilbudsbeskeder (alle butikker)",
			DefaultMemberPermissions: &adminOnly,
		},
		{
			Name:                     "preview_deals",
			Description:              "Forhåndsvis de aktuelle tilbud privat",
			DefaultMemberPermissions: &adminOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "limit",
					Description: "Antal tilbud i previewet (standard: 10, max: 25)",
					Required:    false,
					MinValue:    &minLimit,
					MaxValue:    maxPreview,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "butik",
					Description: "Hvilken butik (standard: spar)",
					Required:    false,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "SPAR", Value: "spar"},
						{Name: "Føtex", Value: "fotex"},
						{Name: "Lidl", Value: "lidl"},
					},
				},
			},
		},
		{
			Name:                     "want",
			Description:              "Administrér delte tilbudsønsker",
			DefaultMemberPermissions: &adminOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "add",
					Description: "Tilføj et ønske",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "id",
							Description: "Kort ID, fx sodavand",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "tekst",
							Description: "Fritekst, fx cola uden sukker",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "remove",
					Description: "Fjern et ønske",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "id",
							Description: "Ønskets ID",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "list",
					Description: "Vis alle ønsker",
				},
			},
		},
		{
			Name:                     "top",
			Description:              "Vis ugens bedste tilbud",
			DefaultMemberPermissions: &adminOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "limit",
					Description: "Antal tilbud (standard: 5, max: 10)",
					Required:    false,
					MinValue:    &minLimit,
					MaxValue:    10,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "butik",
					Description: "Hvilken butik (standard: spar)",
					Required:    false,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "SPAR", Value: "spar"},
						{Name: "Føtex", Value: "fotex"},
						{Name: "Lidl", Value: "lidl"},
					},
				},
			},
		},
		{
			Name:                     "stats",
			Description:              "Vis matcher- og databasestatus",
			DefaultMemberPermissions: &adminOnly,
		},
		{
			Name:                     "concepts",
			Description:              "Administrér synonymer og koncepter til søgningen",
			DefaultMemberPermissions: &adminOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "list",
					Description: "Vis alle koncepter og hvor de kommer fra",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "filter", Description: "Vis kun navne der indeholder teksten", Required: false},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "show",
					Description: "Vis ét koncept med aliaser og udvidelse",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "navn", Description: "Konceptnavn, fx øl", Required: true},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "set",
					Description: "Opret eller tilpas et koncept (gemmes i databasen)",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "navn", Description: "Konceptnavn, fx øl", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "aliases", Description: "Aliasliste, komma-separeret: pilsner, lager", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "refs", Description: "Andre koncepter der hører til, komma-separeret: carlsberg, tuborg", Required: false},
						{Type: discordgo.ApplicationCommandOptionString, Name: "beskrivelse", Description: "Kort beskrivelse", Required: false},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "remove",
					Description: "Skjul et koncept fra søgningen",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "navn", Description: "Konceptnavn", Required: true},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "restore",
					Description: "Nulstil et koncept til concepts.toml",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "navn", Description: "Konceptnavn", Required: true},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "reload",
					Description: "Genindlæs concepts.toml og databasen uden genstart",
				},
			},
		},
		{
			Name:                     "prisalarm",
			Description:              "Få besked når et produkt rammer en målpris",
			DefaultMemberPermissions: &adminOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "add",
					Description: "Opret en prisalarm",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "produkt", Description: "Søgning, fx coca cola -zero", Required: true},
						{Type: discordgo.ApplicationCommandOptionNumber, Name: "pris", Description: "Målpris i kr. Udelad for alarm ved laveste pris nogensinde", Required: false},
						{Type: discordgo.ApplicationCommandOptionString, Name: "butik", Description: "Begræns til én butik", Required: false, Choices: []*discordgo.ApplicationCommandOptionChoice{
							{Name: "Alle", Value: ""}, {Name: "SPAR", Value: "spar"}, {Name: "Føtex", Value: "fotex"}, {Name: "Lidl", Value: "lidl"},
						}},
						{Type: discordgo.ApplicationCommandOptionChannel, Name: "kanal", Description: "Kanal til beskeder (standard: tilbudskanalen)", Required: false},
					},
				},
				{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "list", Description: "Vis alle prisalarmer"},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "remove",
					Description: "Slet en prisalarm",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionInteger, Name: "id", Description: "Alarmens id fra /prisalarm list", Required: true},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "check",
					Description: "Tjek alarmer mod udbuddet nu",
				},
			},
		},
		{
			Name:                     "search",
			Description:              "Søg semantisk i ugens tilbud uden at poste",
			DefaultMemberPermissions: &adminOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "query",
					Description: "Fritekst, fx sukkerfri sodavand",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "limit",
					Description: "Antal resultater (standard: 8, max: 25)",
					Required:    false,
					MinValue:    &minLimit,
					MaxValue:    maxPreview,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "butik",
					Description: "Hvilken butik (standard: spar)",
					Required:    false,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "SPAR", Value: "spar"},
						{Name: "Føtex", Value: "fotex"},
						{Name: "Lidl", Value: "lidl"},
					},
				},
			},
		},
	}

	// When the bot is pinned to one guild, register guild commands: they
	// appear instantly, stay scoped to that guild, and never show up for
	// other servers that invite the bot. Without DISCORD_GUILD_ID the
	// commands are global and propagate to every guild.
	_, err := session.ApplicationCommandBulkOverwrite(appID, b.guildID, commands)
	return err
}

// retailerOption reads a "butik" choice, defaulting to def. Unknown values
// fall back to the default so the bot keeps working if choices drift.
func retailerOption(data discordgo.ApplicationCommandInteractionData, def string) string {
	option := data.GetOption("butik")
	if option == nil {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(option.StringValue())) {
	case service.RetailerFotex:
		return service.RetailerFotex
	case service.RetailerLidl:
		return service.RetailerLidl
	case service.RetailerSPAR:
		return service.RetailerSPAR
	case "alle", "":
		if def == "alle" {
			return "alle"
		}
		return def
	default:
		return def
	}
}

// selectFeeds filters configured feeds by a retailer choice ("alle" = all).
func selectFeeds(config appconfig.BotConfig, retailer string) []feed {
	feeds := feedsFromConfig(config)
	if retailer == "alle" {
		return feeds
	}
	for _, candidate := range feeds {
		if candidate.retailer == retailer {
			return []feed{candidate}
		}
	}
	return nil
}

func (b *DiscordBot) handleSetDealsChannel(session *discordgo.Session, event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	channel := textChannelOption(session, data, "channel")
	if channel == nil {
		b.replyEphemeral(session, event.Interaction, "Vælg en tekstkanal.")
		return
	}

	channelID, err := strconv.ParseInt(channel.ID, 10, 64)
	if err != nil {
		b.replyEphemeral(session, event.Interaction, "Kunne ikke læse kanalens ID.")
		return
	}

	retailer := retailerOption(data, service.RetailerSPAR)
	if _, err := b.configStore.Update(func(config *appconfig.BotConfig) {
		switch retailer {
		case service.RetailerFotex:
			config.FotexChannelID = channelID
		case service.RetailerLidl:
			config.LidlChannelID = channelID
		default:
			config.ChannelID = channelID
		}
	}); err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke gemme kanalen", err))
		return
	}

	b.replyEphemeral(session, event.Interaction, fmt.Sprintf("Gemte %s-kanal: %s", retailerDisplay(retailer), channel.Mention()))
}

func (b *DiscordBot) handleSendDeals(session *discordgo.Session, event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	retailer := retailerOption(data, "alle")
	overrideChannel := textChannelOption(session, data, "channel")
	overrideID := ""
	if overrideChannel != nil {
		overrideID = overrideChannel.ID
	}

	if err := b.deferEphemeral(session, event.Interaction); err != nil {
		b.logger.Error("failed to defer send_deals response", slog.String("error", err.Error()))
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

		feeds := selectFeeds(config, retailer)
		if len(feeds) == 0 {
			b.followupEphemeral(session, event.Interaction, "Ingen butikker er konfigureret (mangler kanal til Føtex eller Lidl?).")
			return
		}
		var summary []string
		for _, feed := range feeds {
			publicationID, err := b.dealService.GetRetailerWeekContext(b.ctx, feed.retailer)
			if err != nil {
				b.logger.Error("error during manual deals send", slog.String("error", err.Error()))
				b.followupEphemeral(session, event.Interaction, b.userError("Fejl under hentning af tilbud", err))
				return
			}
			// Manual sends are incremental: they append only offers this
			// publication has not seen yet and never delete existing posts.
			count, err := b.publishFeed(feed, wants, publicationID, overrideID, true)
			if err != nil {
				b.logger.Error("error during Discord posting", slog.String("error", err.Error()))
				b.followupEphemeral(session, event.Interaction, b.userError("Fejl under posting af tilbud", err))
				return
			}
			summary = append(summary, fmt.Sprintf("%s: %d", retailerDisplay(feed.retailer), count))
		}

		b.followupEphemeral(session, event.Interaction, fmt.Sprintf("Sendte tilbud (%s).", strings.Join(summary, ", ")))
	}()
}

func (b *DiscordBot) handleSearch(session *discordgo.Session, event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	query := ""
	if option := data.GetOption("query"); option != nil {
		query = strings.TrimSpace(option.StringValue())
	}
	limit := 8
	if option := data.GetOption("limit"); option != nil {
		limit = clampInt(int(option.IntValue()), 1, 25)
	}
	if query == "" {
		b.replyEphemeral(session, event.Interaction, "Skriv en søgetekst.")
		return
	}

	retailer := retailerOption(data, service.RetailerSPAR)

	if err := b.deferEphemeral(session, event.Interaction); err != nil {
		b.logger.Error("failed to defer search response", slog.String("error", err.Error()))
		return
	}

	go func() {
		config, err := b.configStore.Load()
		if err != nil {
			b.followupEphemeral(session, event.Interaction, b.userError("Kunne ikke læse config", err))
			return
		}

		b.dealService.SetMatcher(match.ForName(config.Matcher))
		foundDeals, err := b.dealService.SearchDeals(b.ctx, retailer, query, limit)
		if err != nil {
			// Query-language errors carry a user-facing Danish message.
			if parseErr, ok := err.(*search.ParseError); ok {
				b.followupEphemeral(session, event.Interaction, parseErr.UserMessage())
				return
			}
			b.followupEphemeral(session, event.Interaction, b.userError("Fejl under søgning", err))
			return
		}

		b.followupEphemeral(session, event.Interaction, buildPreviewContent(foundDeals, limit))
	}()
}

func (b *DiscordBot) handleClearDeals(session *discordgo.Session, event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if err := b.deferEphemeral(session, event.Interaction); err != nil {
		b.logger.Error("failed to defer clear_deals response", slog.String("error", err.Error()))
		return
	}

	go func() {
		config, err := b.configStore.Load()
		if err != nil {
			b.followupEphemeral(session, event.Interaction, b.userError("Kunne ikke læse config", err))
			return
		}
		if len(config.PostedMessages) == 0 {
			b.followupEphemeral(session, event.Interaction, "Der er ingen gemte tilbudsbeskeder at slette.")
			return
		}

		originalCount := len(config.PostedMessages)
		failures, err := b.clearTrackedMessages(config.PostedMessages)
		if err != nil {
			b.logger.Error("failed to persist cleaned tracking list", slog.String("error", err.Error()))
		}
		if len(failures) > 0 {
			deleted := originalCount - len(failures)
			first := failures[0]
			b.logger.Warn("some tracked messages could not be deleted",
				slog.Int("failed", len(failures)), slog.Int("attempted", originalCount))
			b.followupEphemeral(session, event.Interaction, fmt.Sprintf("Slettede %d af %d beskeder. %d kunne ikke slettes (f.eks. %s:%s: %s). Tjek at botten må slette beskeder i kanalen (inkl. gamle tråde).", deleted, originalCount, len(failures), first.message.ChannelID, first.message.MessageID, first.err))
			return
		}
		b.followupEphemeral(session, event.Interaction, fmt.Sprintf("Slettede %d gemte tilbudsbeskeder.", originalCount))
	}()
}
