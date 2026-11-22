package service

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"spar-tilbuds-bot/internal/match"

	"tjekapi"
)

// unitBatchSize bounds offer-detail requests: the etilbudsavis app cache
// rejects oversized key batches with HTTP 400 (two keys per offer), so
// details are fetched in small batches like cmd/dealfetch does.
const unitBatchSize = 6

// EnrichUnitLabels fills UnitLabel on deals about to be shown ("12,50
// kr/kg") using structured offer details, falling back to a per-liter
// price derived from OCR pack sizes for multipacks. Deals without either
// signal keep an empty label. Context cancellation stops enrichment; a
// failed batch only leaves that batch's labels empty.
func (s *DealService) EnrichUnitLabels(ctx context.Context, deals []match.ScoredDeal) {
	if s == nil || s.tjekAPI == nil || len(deals) == 0 {
		return
	}
	details := s.fetchUnitDetails(ctx, deals)
	for i := range deals {
		deals[i].UnitLabel = s.unitLabelFor(deals[i], details)
	}
}

// fetchUnitDetails batch-fetches offer details keyed by offer id.
func (s *DealService) fetchUnitDetails(ctx context.Context, deals []match.ScoredDeal) map[string]tjekapi.OfferDetails {
	ids := make([]string, 0, len(deals))
	seen := make(map[string]bool, len(deals))
	for _, scored := range deals {
		id := scored.Deal.OfferID
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	details := make(map[string]tjekapi.OfferDetails, len(ids))
	for start := 0; start < len(ids); start += unitBatchSize {
		if err := ctx.Err(); err != nil {
			return details
		}
		end := min(start+unitBatchSize, len(ids))
		batch, err := s.tjekAPI.FetchOfferDetailsBatch(ctx, ids[start:end])
		if err != nil {
			s.logger.Warn("unit price details fetch failed",
				slog.Int("batch_size", end-start), slog.String("error", err.Error()))
			continue
		}
		for id, detail := range batch {
			details[id] = detail
		}
	}
	return details
}

// unitLabelFor resolves one deal's enhedspris display text: structured
// offer data first, OCR pack size second.
func (s *DealService) unitLabelFor(scored match.ScoredDeal, details map[string]tjekapi.OfferDetails) string {
	if detail, ok := details[scored.Deal.OfferID]; ok && detail.UnitPrice != nil && *detail.UnitPrice > 0 {
		return formatUnitLabel(*detail.UnitPrice, detail.BaseUnit)
	}
	return derivedPackLabel(scored.Deal)
}

// derivedPackLabel computes a per-liter price from OCR pack detection
// ("18x33 cl" → kr/l) for multipacks whose offer details are missing.
func derivedPackLabel(deal tjekapi.Deal) string {
	if deal.Price == nil || deal.PackQuantity <= 0 || deal.PackVolumeCL <= 0 {
		return ""
	}
	liters := float64(deal.PackQuantity) * float64(deal.PackVolumeCL) / 100
	if liters <= 0 {
		return ""
	}
	return formatUnitLabel(*deal.Price/liters, "liter")
}

// formatUnitLabel renders a unit price with the Danish decimal comma and
// a unit suffix matching the upstream base unit.
func formatUnitLabel(unit float64, baseUnit string) string {
	suffix := "kr/stk"
	switch strings.ToLower(strings.TrimSpace(baseUnit)) {
	case "kilogram", "kg", "kilo":
		suffix = "kr/kg"
	case "liter", "l", "ltr":
		suffix = "kr/l"
	}
	return strings.ReplaceAll(strconv.FormatFloat(unit, 'f', 2, 64), ".", ",") + " " + suffix
}

// UnitLabelSuffix exposes the unit suffix for tests and diagnostics.
func UnitLabelSuffix(baseUnit string) string {
	switch strings.ToLower(strings.TrimSpace(baseUnit)) {
	case "kilogram", "kg", "kilo":
		return "kr/kg"
	case "liter", "l", "ltr":
		return "kr/l"
	}
	return "kr/stk"
}
