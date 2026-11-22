package discordbot

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/service"
	"tjekapi"
)

func (b *DiscordBot) handlePreviewDeals(session *discordgo.Session, event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	limit := 10
	if option := data.GetOption("limit"); option != nil {
		limit = clampInt(int(option.IntValue()), 1, 25)
	}
	retailer := retailerOption(data, service.RetailerSPAR)

	if err := b.deferEphemeral(session, event.Interaction); err != nil {
		b.logger.Error("failed to defer preview_deals response", "error", err)
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
		foundDeals, err := b.dealService.FetchAndProcessRetailerDeals(b.ctx, retailer, wants)
		if err != nil {
			b.followupEphemeral(session, event.Interaction, b.userError("Fejl under hentning af tilbud", err))
			return
		}

		b.followupEphemeral(session, event.Interaction, buildPreviewContent(foundDeals, limit))
	}()
}

func buildPreviewContent(foundDeals []match.ScoredDeal, limit int) string {
	if len(foundDeals) == 0 {
		return "Ingen matchende tilbud fundet."
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Fandt %d tilbud. Viser %d:\n", len(foundDeals), minInt(limit, len(foundDeals))))

	for index, scored := range foundDeals {
		if index >= limit {
			break
		}
		deal := scored.Deal
		builder.WriteString(fmt.Sprintf("\n%d. %s", index+1, previewName(deal)))
		if deal.Price != nil {
			builder.WriteString(fmt.Sprintf(" - %.0f kr", *deal.Price))
		}
		if deal.IsAppPrice {
			builder.WriteString(" (app-pris)")
		}
		builder.WriteString(fmt.Sprintf(" [%s · %.2f · %s]", scored.WantID, scored.Score, scored.Reason))
	}

	return builder.String()
}

func previewName(deal tjekapi.Deal) string {
	if strings.TrimSpace(deal.CleanName) != "" {
		return strings.TrimSpace(deal.CleanName)
	}
	if strings.TrimSpace(deal.OfferName) != "" {
		return strings.TrimSpace(deal.OfferName)
	}
	return "Ukendt tilbud"
}
