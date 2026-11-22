package match

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"spar-tilbuds-bot/internal/search"

	"tjekapi"
)

// errEmptyWant reports a legacy want with no usable terms or filters;
// such a want matches nothing rather than everything.
var errEmptyWant = errors.New("want has no terms or filters")

// This file is the integration boundary between the bot's want system and
// the isolated search engine: upstream Tjek offers are
// adapted into search.SearchOffer here, and legacy wants are converted
// into engine queries by a compatibility adapter. The
// search package itself knows nothing about Tjek or wants.

// SearchOfferFromHotspot adapts one Tjek hotspot into the engine's
// internal offer representation. Only fields actually present in the
// hotspot are populated; missing structured data never breaks matching.
func SearchOfferFromHotspot(hotspot tjekapi.Hotspot, retailer string) search.SearchOffer {
	parsed := tjekapi.ParseOfferName(hotspot.OfferName)
	offer := search.SearchOffer{
		ID:         hotspot.OfferID,
		Name:       parsed.CleanName,
		Store:      retailer,
		IsAppPrice: parsed.IsAppPrice,
	}
	if parsed.Price != nil {
		ore := search.Money(int64(*parsed.Price*100 + 0.5))
		offer.Price = &ore
	}
	return offer
}

// SearchOfferFromDeal adapts an already-built deal (after OCR enrichment
// filled in unit prices) into the engine's offer representation.
func SearchOfferFromDeal(deal tjekapi.Deal, retailer string) search.SearchOffer {
	offer := search.SearchOffer{
		ID:         deal.OfferID,
		Name:       deal.CleanName,
		Store:      retailer,
		IsAppPrice: deal.IsAppPrice,
	}
	if deal.Price != nil {
		ore := search.Money(int64(*deal.Price*100 + 0.5))
		offer.Price = &ore
	}
	if deal.UnitPrice != nil {
		// Legacy unit prices carry no unit family; the engine compares
		// them under UnitAny unless the query qualifies a family.
		offer.UnitPrice = &search.UnitPrice{
			OrePerBasis: int64(*deal.UnitPrice*100 + 0.5),
			Family:      search.UnitAny,
		}
	}
	return offer
}

// WantQuery builds the engine query equivalent of a want: the compatibility
// adapter between the bot's want model and the search engine.
//
// A want with a raw Query expression uses the full query language. A
// legacy want converts instead: include terms become positive terms with
// ANY semantics (a want's includes have always been alternatives),
// exclude terms become exclusions, and price/unit-price caps become
// canonical filters. Multi-word includes match as phrases, exactly like
// the old matcher. Missing structured data never vetoes on the legacy
// path, preserving the documented contract that unknown prices must not
// hide deals.
func WantQuery(want Want, concepts search.ConceptResolver) (search.CompiledQuery, error) {
	if expr := strings.TrimSpace(want.Query); expr != "" {
		cfg := search.DefaultConfig()
		parsed, err := search.ParseQueryWithConfig(expr, cfg)
		if err != nil {
			return search.CompiledQuery{}, err
		}
		return search.CompileQuery(parsed, concepts, cfg)
	}

	cfg := search.DefaultConfig()
	// Legacy guards pass when data is unknown; keep that contract.
	cfg.MissingFieldFails = false

	query := search.Query{
		Raw:  want.Describe,
		Mode: search.ModeAny,
	}
	for _, include := range want.Include {
		value := strings.TrimSpace(include)
		if value == "" {
			continue
		}
		query.Terms = append(query.Terms, search.Term{
			Value:  value,
			Phrase: strings.Contains(value, " "),
		})
	}
	for _, exclude := range want.Exclude {
		value := strings.TrimSpace(exclude)
		if value == "" {
			continue
		}
		query.Terms = append(query.Terms, search.Term{
			Value:    value,
			Excluded: true,
			Phrase:   strings.Contains(value, " "),
		})
	}
	if want.MaxPrice != nil {
		filter, err := search.ParseFilter("price", "<="+formatDecimal(*want.MaxPrice), cfg)
		if err != nil {
			return search.CompiledQuery{}, err
		}
		query.Filters = append(query.Filters, filter)
	}
	if want.MaxUnitPrice != nil {
		filter, err := search.ParseFilter("unitprice", "<="+formatDecimal(*want.MaxUnitPrice), cfg)
		if err != nil {
			return search.CompiledQuery{}, err
		}
		query.Filters = append(query.Filters, filter)
	}
	if !want.AllowsAppPrice() {
		filter, err := search.ParseFilter("app", "false", cfg)
		if err != nil {
			return search.CompiledQuery{}, err
		}
		query.Filters = append(query.Filters, filter)
	}
	if len(query.Terms) == 0 && len(query.Filters) == 0 {
		// An empty want must match nothing, not everything.
		return search.CompiledQuery{}, errEmptyWant
	}
	return search.CompileQuery(query, concepts, cfg)
}

// formatDecimal renders a float as a plain decimal string for filter
// parsing (decimal points and commas are both accepted downstream).
func formatDecimal(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// defaultConcepts loads the embedded concept registry once for code
// paths without their own resolver (MatchDeal fallbacks, guards). A
// registry installed with SetConcepts (from persisted concept overrides)
// takes precedence.
var (
	defaultConceptsOnce sync.Once
	defaultConcepts     search.ConceptResolver
	overrideConcepts    atomic.Pointer[search.Registry]
)

// SetConcepts installs the registry used by code paths without an
// explicit resolver: matchers built by ForName, MatchDeal and enriched
// guard re-checks. Passing nil removes the override. This is how the
// admin-managed concept store reaches the matcher without threading a
// resolver through every call site.
func SetConcepts(registry *search.Registry) {
	overrideConcepts.Store(registry)
}

// DefaultConcepts returns the active concept registry: the installed
// override, else the embedded registry, else nil (literal-only matching).
func DefaultConcepts() search.ConceptResolver {
	if override := overrideConcepts.Load(); override != nil {
		return override
	}
	defaultConceptsOnce.Do(func() {
		if registry, err := search.DefaultRegistry(); err == nil {
			defaultConcepts = registry
		}
	})
	return defaultConcepts
}

// QueryMatcher is the query-language matcher: it compiles wants through
// the compatibility adapter and evaluates hotspots with the search
// engine. Selected with `matcher = "query"` in config (the default).
type QueryMatcher struct {
	Concepts search.ConceptResolver
	Config   search.Config
	Retailer string
}

// NewQueryMatcher builds the matcher with the active concept registry
// (SetConcepts override, else embedded) and engine defaults.
func NewQueryMatcher() QueryMatcher {
	return QueryMatcher{
		Concepts: DefaultConcepts(),
		Config:   search.DefaultConfig(),
	}
}

// Name implements Matcher.
func (QueryMatcher) Name() string { return MatcherQuery }

// ForRetailer implements RetailerMatcher: returns a copy bound to a feed's
// retailer so store: filters in raw query expressions compare against the
// right canonical store identifier.
func (m QueryMatcher) ForRetailer(retailer string) Matcher {
	m.Retailer = retailer
	return m
}

// Match implements Matcher: for each hotspot the first want whose
// compiled query matches wins, preserving the old ordering contract.
// Each want compiles once per call, not once per offer.
func (m QueryMatcher) Match(ctx context.Context, hotspots []tjekapi.Hotspot, wants []Want) []ScoredDeal {
	compiled := compileWants(wants, m.Concepts)
	out := make([]ScoredDeal, 0, len(hotspots))
	for _, hotspot := range hotspots {
		if err := ctx.Err(); err != nil {
			return out
		}
		parsed := tjekapi.ParseOfferName(hotspot.OfferName)
		prepared := search.Prepare(SearchOfferFromHotspot(hotspot, m.Retailer), m.Config)
		if wantID, reason, nameMatch, ok := matchPrepared(prepared, compiled); ok {
			score := ScoreExact
			if !nameMatch {
				score = ScoreDescriptionOnly
			}
			out = append(out, ScoredDeal{
				Deal: tjekapi.Deal{
					OfferName:        hotspot.OfferName,
					CleanName:        parsed.CleanName,
					Price:            parsed.Price,
					IsAppPrice:       parsed.IsAppPrice,
					MatchingKeywords: []string{wantID},
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
	}
	return out
}

// compiledWant pairs a want with its pre-compiled query so stored
// subscriptions compile once per scan, not once per offer.
type compiledWant struct {
	want     Want
	compiled search.CompiledQuery
	ok       bool
}

func compileWants(wants []Want, concepts search.ConceptResolver) []compiledWant {
	out := make([]compiledWant, 0, len(wants))
	for _, want := range wants {
		compiled, err := WantQuery(want, concepts)
		out = append(out, compiledWant{want: want, compiled: compiled, ok: err == nil})
	}
	return out
}

// matchPrepared returns the first want whose compiled query matches and
// whether that match touched the offer name.
func matchPrepared(offer search.PreparedOffer, wants []compiledWant) (wantID, reason string, nameMatch, ok bool) {
	for _, cw := range wants {
		if !cw.ok {
			continue // invalid stored query: skip, never partially match
		}
		result := search.Match(cw.compiled, offer)
		if !result.Matched {
			continue
		}
		return cw.want.ID, search.FormatReasons(result.Reasons), result.NameMatch, true
	}
	return "", "", false, false
}

// matchDealQuery evaluates one already-built deal against a want with a
// raw query expression (used by MatchDeal and enriched guard re-checks).
func matchDealQuery(deal tjekapi.Deal, want Want, concepts search.ConceptResolver, cfg search.Config) (search.MatchResult, bool) {
	compiled, err := WantQuery(want, concepts)
	if err != nil {
		return search.MatchResult{}, false
	}
	prepared := search.Prepare(SearchOfferFromDeal(deal, ""), cfg)
	result := search.Match(compiled, prepared)
	return result, result.Matched
}
