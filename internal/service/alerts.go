package service

import (
	"context"
	"fmt"
	"log/slog"

	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/search"

	"tjekapi"
)

// AlertHit is one offer that matched an alert query, with the data needed
// to decide whether the alert fires.
type AlertHit struct {
	// QueryIndex is the position of the query in the request.
	QueryIndex int
	// Scored is the matched deal (price, name, price-history key inputs).
	Scored match.ScoredDeal
}

// MatchAlertQueries fetches the retailer's current publication once and
// evaluates every query against every offer — not just the offers the
// wants matched — and returns one hit list per query. Invalid query
// strings are validated at alert-creation time; anything that still fails
// to compile here is skipped with an empty result for that query.
//
// Results are capped per query by limit (0 = no cap).
func (s *DealService) MatchAlertQueries(ctx context.Context, retailer string, queries []string, limit int) ([][]AlertHit, error) {
	compiled := make([]search.CompiledQuery, len(queries))
	usable := make([]bool, len(queries))
	for i, raw := range queries {
		parsed, err := search.ParseQuery(raw)
		if err != nil {
			s.logger.Warn("alert query failed to parse; skipping",
				slog.String("query", raw), slog.String("error", err.Error()))
			continue
		}
		cq, err := search.CompileQuery(parsed, match.DefaultConcepts(), search.DefaultConfig())
		if err != nil {
			s.logger.Warn("alert query failed to compile; skipping",
				slog.String("query", raw), slog.String("error", err.Error()))
			continue
		}
		compiled[i] = cq
		usable[i] = true
	}

	publicationID, err := s.GetRetailerWeekContext(ctx, retailer)
	if err != nil {
		return nil, err
	}
	hotspots, err := s.tjekAPI.FetchAllHotspots(ctx, publicationID)
	if err != nil {
		return nil, err
	}

	cfg := search.DefaultConfig()
	out := make([][]AlertHit, len(queries))
	for _, hotspot := range hotspots {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		parsed := tjekapi.ParseOfferName(hotspot.OfferName)
		prepared := search.Prepare(match.SearchOfferFromHotspot(hotspot, retailer), cfg)
		for i := range queries {
			if !usable[i] {
				continue
			}
			result := search.Match(compiled[i], prepared)
			if !result.Matched {
				continue
			}
			out[i] = append(out[i], AlertHit{
				QueryIndex: i,
				Scored:     scoredFromHotspot(hotspot, parsed, fmt.Sprintf("alarm-%d", i+1), search.FormatReasons(result.Reasons)),
			})
			if limit > 0 && len(out[i]) >= limit {
				usable[i] = false
			}
		}
	}
	return out, nil
}

// scoredFromHotspot builds a display-ready scored deal for an alert hit.
func scoredFromHotspot(hotspot tjekapi.Hotspot, parsed tjekapi.ParsedOfferName, wantID, reason string) match.ScoredDeal {
	return match.ScoredDeal{
		Deal: tjekapi.Deal{
			OfferName:        hotspot.OfferName,
			CleanName:        parsed.CleanName,
			Price:            parsed.Price,
			IsAppPrice:       parsed.IsAppPrice,
			MatchingKeywords: []string{wantID},
			OfferID:          hotspot.OfferID,
			PageNumber:       hotspot.PageNumber,
		},
		WantID: wantID,
		Score:  match.ScoreExact,
		Reason: reason,
	}
}
