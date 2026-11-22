package service

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/store"

	"tjekapi"
)

// Retailer identities used across feeds, wants and price history.
const (
	RetailerSPAR  = "spar"
	RetailerFotex = "fotex"
	RetailerLidl  = "lidl"
)

// Retailers lists every supported feed in display order.
func Retailers() []string {
	return []string{RetailerSPAR, RetailerFotex, RetailerLidl}
}

// maxPrejLookups bounds polite prej.dk scraping per run.
const maxPrejLookups = 15

// prejCallTimeout bounds a single prej.dk search or product fetch.
const prejCallTimeout = 15 * time.Second

// historyWindow is the own-observations baseline window.
const historyWindow = 8 * 7 * 24 * time.Hour

// tjekBusinessName maps a feed retailer to the Tjek business name.
func tjekBusinessName(retailer string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(retailer)) {
	case "", RetailerSPAR:
		return "SPAR", nil
	case RetailerFotex:
		return "Føtex", nil
	case RetailerLidl:
		return "Lidl", nil
	default:
		return "", fmt.Errorf("unknown retailer %q", retailer)
	}
}

// cachedVerdict is the prej_cache payload: verdict plus product identity.
type cachedVerdict struct {
	ProductName string
	ProductURL  string
	Verdict     tjekapi.PrejOfferVerdict
}

func cachedVerdictTOML(c cachedVerdict) string {
	v := c.Verdict
	return strings.Join([]string{
		"product_name = " + strconv.Quote(c.ProductName),
		"product_url = " + strconv.Quote(c.ProductURL),
		"candidate_ore = " + strconv.Itoa(v.CandidateOre),
		"candidate_dkk = " + strconv.FormatFloat(v.CandidateDKK, 'f', -1, 64),
		"cheapest_ore = " + strconv.Itoa(v.CheapestOre),
		"cheapest_chain = " + strconv.Quote(v.CheapestChain),
		"min_history_ore = " + strconv.Itoa(v.MinHistoryOre),
		"pct_above_cheapest = " + strconv.FormatFloat(v.PctAboveCheapest, 'f', -1, 64),
		"pct_above_lowest = " + strconv.FormatFloat(v.PctAboveLowest, 'f', -1, 64),
		"is_cheapest_now = " + strconv.FormatBool(v.IsCheapestNow),
		"is_lowest_ever = " + strconv.FormatBool(v.IsLowestEver),
		"is_good_deal = " + strconv.FormatBool(v.IsGoodDeal),
	}, "\n")
}

func parseCachedVerdictTOML(raw string) (cachedVerdict, error) {
	var c cachedVerdict
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			return c, fmt.Errorf("invalid TOML cache line")
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		parseInt := func() (int, error) { n, err := strconv.Atoi(value); return n, err }
		parseFloat := func() (float64, error) { n, err := strconv.ParseFloat(value, 64); return n, err }
		parseBool := func() (bool, error) { return strconv.ParseBool(value) }
		var err error
		switch key {
		case "product_name":
			c.ProductName, err = strconv.Unquote(value)
		case "product_url":
			c.ProductURL, err = strconv.Unquote(value)
		case "candidate_ore":
			c.Verdict.CandidateOre, err = parseInt()
		case "candidate_dkk":
			c.Verdict.CandidateDKK, err = parseFloat()
		case "cheapest_ore":
			c.Verdict.CheapestOre, err = parseInt()
		case "cheapest_chain":
			c.Verdict.CheapestChain, err = strconv.Unquote(value)
		case "min_history_ore":
			c.Verdict.MinHistoryOre, err = parseInt()
		case "pct_above_cheapest":
			c.Verdict.PctAboveCheapest, err = parseFloat()
		case "pct_above_lowest":
			c.Verdict.PctAboveLowest, err = parseFloat()
		case "is_cheapest_now":
			c.Verdict.IsCheapestNow, err = parseBool()
		case "is_lowest_ever":
			c.Verdict.IsLowestEver, err = parseBool()
		case "is_good_deal":
			c.Verdict.IsGoodDeal, err = parseBool()
		default:
			return c, fmt.Errorf("unknown TOML cache key %q", key)
		}
		if err != nil {
			return c, err
		}
	}
	return c, scanner.Err()
}

// finishDeals resolves enriched deals against wants (carrying forward the
// matcher's scores), records own price observations, adds prej.dk verdicts
// and ranks by relevance × discount. Every matched deal is returned —
// nothing is capped or hidden by score.
func (s *DealService) finishDeals(ctx context.Context, retailer, publicationID string, wants []match.Want, prior map[string]match.ScoredDeal, deals []tjekapi.Deal) []match.ScoredDeal {
	resolved := resolveDeals(wants, prior, deals)
	s.recordObservations(retailer, publicationID, resolved)
	ranked := s.enrichPrej(ctx, retailer, publicationID, resolved)
	s.EnrichUnitLabels(ctx, ranked)
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].RankScore() > ranked[b].RankScore() })
	return ranked
}

// resolveDeals keeps prior matcher scores for enriched deals instead of
// re-running full matching per deal. Enriched guards (e.g. OCR unit prices)
// still apply; anything they reject falls back to a fresh match so later
// wants can still claim the deal.
func resolveDeals(wants []match.Want, prior map[string]match.ScoredDeal, deals []tjekapi.Deal) []match.ScoredDeal {
	resolved := make([]match.ScoredDeal, 0, len(deals))
	for _, deal := range deals {
		if deal.Price == nil || *deal.Price <= 0 {
			continue
		}
		if sd, ok := resolvePriorDeal(wants, prior, deal); ok {
			resolved = append(resolved, sd)
			continue
		}
		if sd, ok := match.MatchDeal(deal, wants); ok {
			resolved = append(resolved, sd)
		}
	}
	return resolved
}

func resolvePriorDeal(wants []match.Want, prior map[string]match.ScoredDeal, deal tjekapi.Deal) (match.ScoredDeal, bool) {
	prev, ok := prior[dealKey(deal)]
	if !ok {
		return match.ScoredDeal{}, false
	}
	sd := prev
	sd.Deal = deal
	if len(sd.Deal.MatchingKeywords) == 0 {
		if len(prev.Deal.MatchingKeywords) > 0 {
			sd.Deal.MatchingKeywords = append([]string(nil), prev.Deal.MatchingKeywords...)
		} else {
			return match.ScoredDeal{}, false
		}
	}
	if !match.PassesEnrichedGuards(sd.Deal, wants) {
		return match.ScoredDeal{}, false
	}
	return sd, true
}

// rankDeals sorts by descending rank score and caps the feed.
func rankDeals(deals []match.ScoredDeal, cap int) []match.ScoredDeal {
	sort.SliceStable(deals, func(a, b int) bool { return deals[a].RankScore() > deals[b].RankScore() })
	if cap > 0 && len(deals) > cap {
		deals = deals[:cap]
	}
	return deals
}

// recordObservations stores hotspot sightings for the own-history baseline.
// Failures only log: history must never block posting.
func (s *DealService) recordObservations(retailer, publicationID string, deals []match.ScoredDeal) {
	db := s.storeDB()
	if db == nil {
		return
	}
	observations := make([]store.PriceObservation, 0, len(deals))
	for _, sd := range deals {
		if sd.Deal.Price == nil || *sd.Deal.Price <= 0 {
			continue
		}
		key := match.NormKey(sd.Deal.CleanName, sd.Deal.PackQuantity, sd.Deal.PackVolumeCL)
		if key == "" {
			continue
		}
		observations = append(observations, store.PriceObservation{
			NormKey: key, Price: *sd.Deal.Price, UnitPrice: sd.Deal.UnitPrice,
			PublicationID: publicationID, Source: "hotspot",
		})
	}
	if err := db.RecordObservations(retailer, observations); err != nil {
		s.logger.Warn("record price observations failed",
			slog.Int("count", len(observations)), slog.String("error", err.Error()))
	}
}

// enrichPrej attaches prej.dk verdicts to the top deals by relevance.
// Opportunistic by design: no hit, fetch error or missing store leaves the
// deal untouched (neutral signal, no badge). Cached per publication.
// Identical products at identical prices share one lookup.
func (s *DealService) enrichPrej(ctx context.Context, retailer, publicationID string, deals []match.ScoredDeal) []match.ScoredDeal {
	if s.storeDB() == nil {
		return deals
	}
	// Rank indexes by relevance so lookups go to the most relevant first.
	ordered := make([]int, len(deals))
	for i := range deals {
		ordered[i] = i
	}
	sort.SliceStable(ordered, func(a, b int) bool { return deals[ordered[a]].Score > deals[ordered[b]].Score })
	type lookup struct {
		sample  match.ScoredDeal
		members []*match.ScoredDeal
	}
	groups := make([]*lookup, 0, len(deals))
	byKey := make(map[string]*lookup, len(deals))
	for _, idx := range ordered {
		if len(groups) >= maxPrejLookups {
			break
		}
		if err := ctx.Err(); err != nil {
			break
		}
		key, ok := prejGroupKey(retailer, deals[idx])
		if !ok {
			continue
		}
		group, ok := byKey[key]
		if !ok {
			group = &lookup{sample: deals[idx]}
			byKey[key] = group
			groups = append(groups, group)
		}
		group.members = append(group.members, &deals[idx])
	}
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			break
		}
		cached, ok := s.fetchPrejVerdict(ctx, retailer, publicationID, group.sample)
		if !ok {
			continue
		}
		for _, target := range group.members {
			s.applyVerdict(retailer, target, *target, cached)
		}
	}
	return deals
}

// prejGroupKey groups deals that share one prej.dk lookup: same retailer,
// normalized product and exact price. Verdicts embed the candidate price,
// so prices must match for sharing to stay correct.
func prejGroupKey(retailer string, sd match.ScoredDeal) (string, bool) {
	if sd.Deal.Price == nil || *sd.Deal.Price <= 0 {
		return "", false
	}
	key := match.NormKey(sd.Deal.CleanName, sd.Deal.PackQuantity, sd.Deal.PackVolumeCL)
	if key == "" {
		return "", false
	}
	return prejCacheKey(retailer, key, *sd.Deal.Price), true
}

// prejCacheKey namespaces cached verdicts by retailer, product and price so
// a verdict computed for one candidate price is never reused for another.
func prejCacheKey(retailer, normKey string, price float64) string {
	return retailer + "\n" + normKey + "\n" + strconv.FormatFloat(price, 'g', -1, 64)
}

func dealKey(deal tjekapi.Deal) string {
	if deal.OfferID != "" {
		return deal.OfferID
	}
	return deal.OfferName
}

// fetchPrejVerdict returns the cached or freshly scraped verdict for one
// scored deal. The second return is false when there is no confident product,
// the fetch fails, or the store is missing — all opportunistic, never fatal.
func (s *DealService) fetchPrejVerdict(ctx context.Context, retailer, publicationID string, sd match.ScoredDeal) (cachedVerdict, bool) {
	if sd.Deal.Price == nil || *sd.Deal.Price <= 0 {
		return cachedVerdict{}, false
	}
	key := match.NormKey(sd.Deal.CleanName, sd.Deal.PackQuantity, sd.Deal.PackVolumeCL)
	if key == "" {
		return cachedVerdict{}, false
	}
	db := s.storeDB()
	if db == nil {
		return cachedVerdict{}, false
	}
	cacheKey := prejCacheKey(retailer, key, *sd.Deal.Price)
	if raw, ok, err := db.GetPrejVerdict(publicationID, cacheKey); err != nil {
		s.logger.Warn("prej cache read failed", slog.String("error", err.Error()))
	} else if ok {
		cached, err := parseCachedVerdictTOML(raw)
		if err == nil {
			return cached, true
		}
		s.logger.Warn("prej cache decode failed, refetching", slog.String("key", key))
	}
	callCtx, cancel := context.WithTimeout(ctx, prejCallTimeout)
	defer cancel()
	results, err := s.tjekAPI.FetchPrejSearch(callCtx, sd.Deal.CleanName)
	if err != nil {
		s.logger.Warn("prej search failed", slog.String("deal", sd.Deal.CleanName), slog.String("error", err.Error()))
		return cachedVerdict{}, false
	}
	best, ok := pickPrejResult(sd.Deal.CleanName, results)
	if !ok {
		return cachedVerdict{}, false
	}
	product, err := s.tjekAPI.FetchPrejProduct(callCtx, best.URL)
	if err != nil {
		s.logger.Warn("prej product fetch failed", slog.String("url", best.URL), slog.String("error", err.Error()))
		return cachedVerdict{}, false
	}
	cached := cachedVerdict{
		ProductName: product.Name,
		ProductURL:  product.URL,
		Verdict:     tjekapi.ComparePrejOffer(product, *sd.Deal.Price),
	}
	if err := db.PutPrejVerdict(publicationID, cacheKey, cachedVerdictTOML(cached)); err != nil {
		s.logger.Warn("prej cache write failed", slog.String("error", err.Error()))
	}
	return cached, true
}

// pickPrejResult chooses the search result sharing the most significant
// tokens with the deal name. One shared token of 4+ letters is required;
// otherwise there is no confident product and the deal stays verdict-less.
func pickPrejResult(cleanName string, results []tjekapi.PrejSearchResult) (tjekapi.PrejSearchResult, bool) {
	want := significantTokens(cleanName)
	if len(want) == 0 {
		return tjekapi.PrejSearchResult{}, false
	}
	best := -1
	bestScore := 0
	for i, result := range results {
		score := 0
		have := significantTokens(result.Name)
		for token := range want {
			if _, ok := have[token]; ok {
				score += len(token)
				continue
			}
			for other := range have {
				if len(token) >= 4 && (strings.Contains(other, token) || strings.Contains(token, other)) {
					score += len(token) / 2
					break
				}
			}
		}
		if score <= 0 {
			continue
		}
		if result.OnOffer {
			score++
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return tjekapi.PrejSearchResult{}, false
	}
	return results[best], true
}

func significantTokens(name string) map[string]bool {
	out := map[string]bool{}
	for _, token := range strings.Fields(match.NormalizeText(name)) {
		if len([]rune(token)) >= 3 {
			out[token] = true
		}
	}
	return out
}

// applyVerdict folds a verdict into score, badges and discount.
func (s *DealService) applyVerdict(retailer string, target *match.ScoredDeal, sd match.ScoredDeal, cached cachedVerdict) {
	verdict := cached.Verdict
	signal := discountSignal(verdict)
	target.Final = sd.Score * signal
	target.PrejURL = cached.ProductURL
	target.Badges = prejBadges(verdict)
	if discount, ok := s.sqliteDiscount(retailer, target, sd); ok {
		if discount > target.Discount {
			target.Discount = discount
		}
		if signal < 0.9 {
			target.Final = sd.Score * maxFloat(signal, 0.9)
		}
	}
	s.logger.Info("prej verdict applied",
		slog.String("deal", sd.Deal.CleanName),
		slog.String("product", cached.ProductName),
		slog.Float64("signal", signal),
		slog.Any("badges", target.Badges))
}

// sqliteDiscount compares against the own-history average.
func (s *DealService) sqliteDiscount(retailer string, target *match.ScoredDeal, sd match.ScoredDeal) (float64, bool) {
	db := s.storeDB()
	if db == nil || sd.Deal.Price == nil {
		return 0, false
	}
	key := match.NormKey(sd.Deal.CleanName, sd.Deal.PackQuantity, sd.Deal.PackVolumeCL)
	_, avg, n, ok, err := db.BaselineSince(retailer, key, time.Now().Add(-historyWindow))
	if err != nil || !ok || n < 2 || avg <= 0 {
		return 0, false
	}
	price := *sd.Deal.Price
	if price >= avg*0.8 {
		return 0, false
	}
	discount := (avg - price) / avg
	target.Badges = append(target.Badges, formatPercentBadge("vs snit (8 uger)", discount))
	return discount, true
}

func formatPercentBadge(label string, fraction float64) string {
	return fmt.Sprintf("-%d%% %s", int(fraction*100+0.5), label)
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// discountSignal maps a verdict to 0.5..1.0. Unknown history is neutral:
// it never penalizes, only confirmed premiums sink a deal.
func discountSignal(verdict tjekapi.PrejOfferVerdict) float64 {
	if verdict.IsLowestEver {
		return 1.0
	}
	signal := 1.0 - minFloat(verdict.PctAboveLowest, 50)/100
	if verdict.IsGoodDeal && signal < 0.9 {
		signal = 0.9
	}
	if verdict.IsCheapestNow && signal < 0.85 {
		signal = 0.85
	}
	return signal
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// prejBadges renders Danish price highlights. Only good news; premiums stay
// silent to avoid clutter.
func prejBadges(verdict tjekapi.PrejOfferVerdict) []string {
	var badges []string
	if verdict.IsLowestEver {
		badges = append(badges, "billigste i 30 dage")
	} else if verdict.IsCheapestNow {
		badges = append(badges, "billigste lige nu")
	}
	if verdict.IsGoodDeal && !verdict.IsLowestEver {
		badges = append(badges, "god deal")
	}
	return badges
}
