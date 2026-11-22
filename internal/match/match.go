// Package match implements the bot's own deal matching: admin-managed wants
// evaluated against Tjek hotspots with strict keyword rules.
//
// There is deliberately no LLM, no embedding and no category expansion
// anywhere in this package. Every score and reason string is deterministic
// so `/preview_deals` output stays explainable and free of surprise hits
// (tortilla wraps are not chips, Premier Is is not Smirnoff Ice).
package match

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"spar-tilbuds-bot/internal/search"

	"tjekapi"
)

// Matcher names selectable through config (`matcher = ...`). The query
// matcher is the engine-backed default; "keywords" keeps the strict
// keyword matcher, and "normalized"/"semantic" stay as deprecated
// aliases so old configs keep working without surprises.
const (
	MatcherKeywords   = "keywords"
	MatcherNormalized = "normalized"
	MatcherSemantic   = "semantic"
	MatcherQuery      = "query"
)

// DefaultMatcher is used when config does not select a matcher. The
// query-language engine is the integration default; the
// legacy matchers stay selectable by name.
const DefaultMatcher = MatcherQuery

// DefaultThreshold is kept for config round-trip compatibility. The strict
// matcher ignores thresholds: a want either matches by keyword or it does
// not. Tune matching with explicit include/exclude terms via /preview.
const DefaultThreshold = 0.45

// ScoreExact is the score of every strict keyword hit in the offer name.
const ScoreExact = 1.0

// ScoreDescriptionOnly is the score of a hit found only in wider metadata
// (description, attributes). Such hits still match — recall matters — but
// rank below product-name hits so "ost" does not outrank a cheese when it
// only appears in a beer description.
const ScoreDescriptionOnly = 0.55

// Want is one admin-managed interest. All wants are shared by the whole
// channel feed; there are no per-user wants.
type Want struct {
	ID       string
	Describe string
	// Query is an optional raw query-language expression. When set it
	// takes precedence over Include/Exclude and the numeric caps below.
	Query         string
	Include       []string
	Exclude       []string
	MaxPrice      *float64
	MaxUnitPrice  *float64
	AllowAppPrice *bool // nil = app prices allowed (current behavior)
	Threshold     float64
	Retailers     []string // empty = all feeds ("spar", "fotex", "lidl")
}

// AllowsAppPrice reports whether app-price offers may match the want.
func (w Want) AllowsAppPrice() bool {
	return w.AllowAppPrice == nil || *w.AllowAppPrice
}

// AppliesToRetailer reports whether the want participates in a retailer's feed.
func (w Want) AppliesToRetailer(retailer string) bool {
	if len(w.Retailers) == 0 {
		return true
	}
	for _, r := range w.Retailers {
		if strings.EqualFold(strings.TrimSpace(r), retailer) {
			return true
		}
	}
	return false
}

// EffectiveThreshold resolves a zero Threshold to the global default.
func (w Want) EffectiveThreshold() float64 {
	if w.Threshold > 0 {
		return w.Threshold
	}
	return DefaultThreshold
}

// ScoredDeal is one hotspot that matched a want.
type ScoredDeal struct {
	Deal   tjekapi.Deal
	WantID string
	// Score is the match relevance in 0..1 (rules) or cosine similarity.
	Score float64
	// Reason is the deterministic match explanation shown in previews.
	Reason string
	// Final is relevance × discount signal, set by the service ranker.
	// Zero means unranked (rank equals Score).
	Final float64
	// Discount is the fraction below the price reference, if known.
	Discount float64
	// Badges are Danish price highlights for embeds ("billigste i 30 dage").
	Badges []string
	// PrejURL links the matched prej.dk product, when one was found.
	PrejURL string
	// UnitLabel is the display-ready unit price ("12,50 kr/kg"), filled
	// from offer details or derived from OCR pack sizes.
	UnitLabel string
}

// RankScore returns the ordering score: Final when ranked, else Score.
func (sd ScoredDeal) RankScore() float64 {
	if sd.Final > 0 {
		return sd.Final
	}
	return sd.Score
}

// NormKey builds the price-history key for a deal: normalized clean name
// plus OCR pack size, so "Pepsi Max 18x33cl" and "Pepsi Max 6x33cl" track
// separately. Retailers namespace their own histories.
func NormKey(cleanName string, packQuantity, packVolumeCL int) string {
	key := normalize(cleanName)
	if packQuantity > 1 && packVolumeCL > 0 {
		key += formatPackSuffix(packQuantity, packVolumeCL)
	}
	return key
}

func formatPackSuffix(quantity, volumeCL int) string {
	return " |" + strconv.Itoa(quantity) + "x" + strconv.Itoa(volumeCL) + "cl"
}

// SortByScoreDesc orders scored deals by descending score, stable.
func SortByScoreDesc(deals []ScoredDeal) {
	for a := 1; a < len(deals); a++ {
		for b := a; b > 0 && deals[b].Score > deals[b-1].Score; b-- {
			deals[b], deals[b-1] = deals[b-1], deals[b]
		}
	}
}

// Matcher evaluates hotspots against wants.
type Matcher interface {
	Name() string
	Match(ctx context.Context, hotspots []tjekapi.Hotspot, wants []Want) []ScoredDeal
}

// RetailerMatcher is implemented by matchers that compare against a feed's
// retailer, for example to resolve `store:` filters in a query expression. The
// service rebinds a matcher per feed so the same engine sees the right store.
type RetailerMatcher interface {
	Matcher
	ForRetailer(retailer string) Matcher
}

// ForName returns the matcher selected by name. "normalized" and "semantic"
// are deprecated aliases of the strict keyword matcher, kept so configs written
// for the removed alias/category/fuzzy/embedding matchers keep posting the same
// results instead of silently switching to the query engine. Unknown names fall
// back to the default query matcher.
func ForName(name string) Matcher {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case MatcherKeywords, MatcherNormalized, MatcherSemantic:
		return KeywordMatcher{}
	default:
		// "query", empty and unknown names follow the default matcher.
		return NewQueryMatcher()
	}
}

// WantsFromKeywords converts legacy flat keywords into one normalized want
// per keyword, preserving old behavior as closely as possible.
func WantsFromKeywords(keywords []string) []Want {
	wants := make([]Want, 0, len(keywords))
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		wants = append(wants, Want{
			ID:        keyword,
			Include:   []string{keyword},
			Threshold: DefaultThreshold,
		})
	}
	return wants
}

// FilterWantsByRetailer keeps the wants participating in a retailer's feed.
func FilterWantsByRetailer(wants []Want, retailer string) []Want {
	filtered := make([]Want, 0, len(wants))
	for _, want := range wants {
		if want.AppliesToRetailer(retailer) {
			filtered = append(filtered, want)
		}
	}
	return filtered
}

// KeywordMatcher is the only matcher: strict word/phrase hits with Danish
// normalization, exclusions and price guards. No aliases, no categories,
// no fuzzy matching, no embeddings.
type KeywordMatcher struct{}

// Name implements Matcher.
func (KeywordMatcher) Name() string { return MatcherKeywords }

// Match implements Matcher.
func (KeywordMatcher) Match(ctx context.Context, hotspots []tjekapi.Hotspot, wants []Want) []ScoredDeal {
	out := make([]ScoredDeal, 0, len(hotspots))
	for _, hotspot := range hotspots {
		if err := ctx.Err(); err != nil {
			return out
		}
		parsed := tjekapi.ParseOfferName(hotspot.OfferName)
		doc := newMatchDoc(parsed.CleanName, hotspot.OfferName)
		wantID, term, reason, score, ok := matchWants(doc, parsed.Price, parsed.IsAppPrice, wants)
		if !ok {
			continue
		}
		out = append(out, ScoredDeal{
			Deal: tjekapi.Deal{
				OfferName:        hotspot.OfferName,
				CleanName:        parsed.CleanName,
				Price:            parsed.Price,
				IsAppPrice:       parsed.IsAppPrice,
				MatchingKeywords: []string{term},
				OfferID:          hotspot.OfferID,
				X1:               hotspot.X1,
				Y1:               hotspot.Y1,
				X2:               hotspot.X2,
				Y2:               hotspot.Y2,
				PageNumber:       hotspot.PageNumber,
			},
			WantID: wantID,
			Score:  score,
			Reason: reason,
		})
	}
	return out
}

// ownsTerm reports whether term is one of the want's include terms.
func ownsTerm(want Want, term string) bool {
	for _, include := range want.Include {
		if strings.EqualFold(strings.TrimSpace(include), strings.TrimSpace(term)) {
			return true
		}
	}
	return false
}

// ownsKeyword reports whether a recorded matching keyword identifies the
// want: either one of its include terms or its ID (query-expression
// wants record the want ID as their match keyword).
func ownsKeyword(want Want, keyword string) bool {
	return want.ID == keyword || ownsTerm(want, keyword)
}

// MatchDeal evaluates one already-built deal (for example after
// GroupAndDeduplicateOffers) against wants, keeping the deal as-is.
// Wants with a raw query expression route through the search engine so
// re-resolution after dedup uses the same semantics as the first match.
func MatchDeal(deal tjekapi.Deal, wants []Want) (ScoredDeal, bool) {
	for _, want := range wants {
		if strings.TrimSpace(want.Query) != "" {
			if sd, ok := matchDealWant(deal, want); ok {
				return sd, true
			}
			continue
		}
		doc := newMatchDoc(deal.CleanName, deal.OfferName)
		wantID, term, reason, score, ok := matchWants(doc, deal.Price, deal.IsAppPrice, []Want{want})
		if !ok {
			continue
		}
		if len(deal.MatchingKeywords) == 0 {
			deal.MatchingKeywords = []string{term}
		}
		return ScoredDeal{Deal: deal, WantID: wantID, Score: score, Reason: reason}, true
	}
	return ScoredDeal{}, false
}

// matchDealWant runs one query-expression want against a built deal.
func matchDealWant(deal tjekapi.Deal, want Want) (ScoredDeal, bool) {
	result, ok := matchDealQuery(deal, want, DefaultConcepts(), search.DefaultConfig())
	if !ok {
		return ScoredDeal{}, false
	}
	if len(deal.MatchingKeywords) == 0 {
		deal.MatchingKeywords = []string{want.ID}
	}
	return ScoredDeal{
		Deal:   deal,
		WantID: want.ID,
		Score:  ScoreExact,
		Reason: search.FormatReasons(result.Reasons),
	}, true
}

// passesGuards enforces the want's price and app-price constraints.
// Unknown prices never veto: missing data must not hide relevant deals.
func passesGuards(price, unitPrice *float64, isAppPrice bool, want Want) bool {
	if !want.AllowsAppPrice() && isAppPrice {
		return false
	}
	if want.MaxPrice != nil && price != nil && *price > *want.MaxPrice {
		return false
	}
	if want.MaxUnitPrice != nil && unitPrice != nil && *unitPrice > *want.MaxUnitPrice {
		return false
	}
	return true
}

// PassesEnrichedGuards re-checks guards once OCR/image enrichment has
// filled in unit prices. Deals with unknown unit prices still pass.
// Query-expression wants re-run the full engine match, so their
// unit-price and price filters apply to the enriched values.
func PassesEnrichedGuards(deal tjekapi.Deal, wants []Want) bool {
	want := wantByID(wants, dealWantedID(deal, wants))
	if strings.TrimSpace(want.Query) != "" {
		_, ok := matchDealQuery(deal, want, DefaultConcepts(), search.DefaultConfig())
		return ok
	}
	return passesGuards(deal.Price, deal.UnitPrice, deal.IsAppPrice, want)
}

func dealWantedID(deal tjekapi.Deal, wants []Want) string {
	for _, want := range wants {
		for _, keyword := range deal.MatchingKeywords {
			if ownsKeyword(want, keyword) {
				return want.ID
			}
		}
	}
	return ""
}

func wantByID(wants []Want, id string) Want {
	for _, want := range wants {
		if want.ID == id {
			return want
		}
	}
	return Want{ID: id}
}

// matchDoc is the normalized text of one offer.
type matchDoc struct {
	norm    string
	compact string // norm without spaces, for Danish compounds
	tokens  map[string]struct{}
	// sorted lists iteration over tokens, so a compound hit reports the same
	// token (and therefore the same reason string) on every run.
	tokenList []string
}

func newMatchDoc(cleanName, offerName string) matchDoc {
	norm := normalize(cleanName + " " + offerName)
	tokens := make(map[string]struct{})
	tokenList := make([]string, 0, 8)
	for _, token := range strings.Fields(norm) {
		if _, seen := tokens[token]; seen {
			continue
		}
		tokens[token] = struct{}{}
		tokenList = append(tokenList, token)
	}
	sort.Strings(tokenList)
	return matchDoc{norm: norm, compact: strings.ReplaceAll(norm, " ", ""), tokens: tokens, tokenList: tokenList}
}

// matchWants evaluates wants in order and returns the first hit whose
// guards also pass, so a price/app veto never hides a deal from later wants.
func matchWants(doc matchDoc, price *float64, isAppPrice bool, wants []Want) (wantID, term, reason string, score float64, ok bool) {
	for _, want := range wants {
		if vetoed(doc, want) {
			continue
		}
		term, reason, score, hit := matchIncludes(doc, want)
		if !hit {
			continue
		}
		if !passesGuards(price, nil, isAppPrice, want) {
			continue
		}
		return want.ID, term, reason, score, true
	}
	return "", "", "", 0, false
}

// vetoed reports whether any exclude term of the want matches the doc.
// Exclusions are strict on purpose: plain tokens or phrases only,
// no aliases and no fuzziness.
func vetoed(doc matchDoc, want Want) bool {
	for _, exclude := range want.Exclude {
		term := stripQuotes(normalize(exclude))
		if term == "" {
			continue
		}
		if strings.Contains(term, " ") {
			if strings.Contains(doc.norm, term) {
				return true
			}
			continue
		}
		if _, found := doc.tokens[term]; found {
			return true
		}
	}
	return false
}

// matchIncludes tries strict exact matching only: plain tokens and "quoted
// phrases" with Danish normalization. Anything else is not a hit.
func matchIncludes(doc matchDoc, want Want) (term, reason string, score float64, ok bool) {
	for _, include := range want.Include {
		if term, reason, score, hit := matchExact(doc, include); hit {
			return term, reason, score, true
		}
	}
	return "", "", 0, false
}

// matchExact matches plain tokens and "quoted phrases".
func matchExact(doc matchDoc, include string) (term, reason string, score float64, ok bool) {
	term = strings.TrimSpace(include)
	normalized := stripQuotes(normalize(term))
	if normalized == "" {
		return "", "", 0, false
	}
	if strings.Contains(normalized, " ") {
		// Phrases match with spaces ("smirnoff ice") and joined, so Danish
		// compounds ("chokoladebar" for "chokolade bar") still hit.
		if strings.Contains(doc.norm, normalized) {
			return term, `frase "` + normalized + `"`, ScoreExact, true
		}
		joined := strings.ReplaceAll(normalized, " ", "")
		if strings.Contains(doc.compact, joined) {
			return term, `frase "` + normalized + `"`, ScoreExact, true
		}
		return "", "", 0, false
	}
	if _, found := doc.tokens[normalized]; found {
		return term, `ord "` + normalized + `"`, ScoreExact, true
	}
	// Long single words may hide inside a compound token
	// ("chokolade" in "chokoladebar"). Short words stay exact-only to
	// avoid mush ("øl" must not match "pølse").
	if len([]rune(normalized)) >= 6 {
		for _, token := range doc.tokenList {
			if strings.Contains(token, normalized) {
				return term, `ord "` + normalized + `" ⊆ "` + token + `"`, ScoreExact, true
			}
		}
	}
	return "", "", 0, false
}

// foldReplacer maps Danish and common accented letters to ASCII so typos
// and missing keyboards still match ("snøfler" ≡ "snoefler").
var foldReplacer = strings.NewReplacer(
	"æ", "ae", "ø", "oe", "å", "aa",
	"ä", "ae", "ö", "oe", "ü", "u",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"á", "a", "à", "a", "â", "a", "ã", "a",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o",
	"ú", "u", "ù", "u", "û", "u",
	"ý", "y", "ÿ", "y", "ç", "c", "ñ", "n", "ß", "ss",
)

// normalize lowercases, folds diacritics and reduces every run of
// non-letters/digits to a single space.
func normalize(s string) string {
	s = foldReplacer.Replace(strings.ToLower(s))
	var builder strings.Builder
	builder.Grow(len(s))
	needSpace := false
	wrote := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if needSpace && wrote {
				builder.WriteByte(' ')
			}
			builder.WriteRune(r)
			needSpace, wrote = false, true
		} else {
			needSpace = wrote
		}
	}
	return builder.String()
}

// NormalizeText exposes Danish normalization (lowercase, diacritic folding,
// punctuation to spaces) for cross-package text comparison.
func NormalizeText(s string) string {
	return normalize(s)
}

// stripQuotes removes one pair of surrounding double quotes.
func stripQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}
