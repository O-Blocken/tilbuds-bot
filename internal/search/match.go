package search

import "strings"

// ReasonKind classifies why an offer matched or was rejected.
type ReasonKind int

// Reason kinds recorded in MatchReason.
const (
	// ReasonTerm is a literal word hit.
	ReasonTerm ReasonKind = iota
	// ReasonPhrase is a quoted phrase hit.
	ReasonPhrase
	// ReasonAlias is a hit through a configured concept alias.
	ReasonAlias
	// ReasonConcept is a hit where the term itself is a concept and the
	// offer matched one of its aliases.
	ReasonConcept
	// ReasonFilter is a satisfied structured filter.
	ReasonFilter
)

// String returns a short Danish label for the reason kind.
func (k ReasonKind) String() string {
	switch k {
	case ReasonPhrase:
		return "frase"
	case ReasonAlias:
		return "alias"
	case ReasonConcept:
		return "koncept"
	case ReasonFilter:
		return "filter"
	}
	return "ord"
}

// MatchReason explains one contributing match or rejection.
type MatchReason struct {
	Kind ReasonKind
	// Term is the user term or filter key the reason is about.
	Term string
	// Detail is the concrete hit: matched alias, filter expression, or
	// matched offer token.
	Detail string
	// InName reports that the hit was inside the offer name. Hits found
	// only in wider metadata (description, attributes) are weaker
	// evidence and rank lower; filters always set InName.
	InName bool
}

// MatchResult is a structured evaluation outcome.
type MatchResult struct {
	Matched bool
	// NameMatch reports that at least one positive term matched inside
	// the offer name (or that the query has no positive terms).
	// Description-only hits set it to false.
	NameMatch bool
	// Reasons explains why the offer matched. Populated on success and,
	// in debug mode, for the first rejection cause as well.
	Reasons []MatchReason
	// RejectedBy identifies the exclusion or failed filter that rejected
	// the offer. Only populated in debug mode.
	RejectedBy []MatchReason
}

// Match evaluates a compiled query against a prepared offer. Bulk
// matching stays cheap: no rejection detail is collected unless you call
// MatchDebug.
func Match(query CompiledQuery, offer PreparedOffer) MatchResult {
	return match(query, offer, false)
}

// MatchDebug is Match with full rejection diagnostics.
func MatchDebug(query CompiledQuery, offer PreparedOffer) MatchResult {
	return match(query, offer, true)
}

func match(query CompiledQuery, offer PreparedOffer, debug bool) MatchResult {
	// Hard exclusions first: any exclusion hit rejects immediately,
	// overriding every positive match. Exclusions use
	// the same concept expansion as positives: an excluded concept
	// rejects through any configured alias.
	for _, term := range query.Negative {
		if alias, token, inName, hit := termMatches(term, offer); hit {
			result := MatchResult{Matched: false}
			if debug {
				result.RejectedBy = []MatchReason{{
					Kind:   reasonKindFor(term, alias),
					Term:   term.Original,
					Detail: explainHit(alias, token),
					InName: inName,
				}}
			}
			return result
		}
	}

	// Structured filters are hard constraints on known fields.
	for _, filter := range query.Filters {
		if !filter.Matches(offer, query.MissingFieldFails) {
			result := MatchResult{Matched: false}
			if debug {
				result.RejectedBy = []MatchReason{{
					Kind:   ReasonFilter,
					Term:   filter.Key(),
					Detail: filter.Explain(),
					InName: true,
				}}
			}
			return result
		}
	}

	// Positive terms: ALL must match (default) or ANY may match.
	reasons := make([]MatchReason, 0, len(query.Positive))
	if len(query.Positive) == 0 {
		// A filters-only query matches every offer that survives the
		// filters.
		for _, filter := range query.Filters {
			reasons = append(reasons, MatchReason{
				Kind:   ReasonFilter,
				Term:   filter.Key(),
				Detail: filter.Explain(),
				InName: true,
			})
		}
		return MatchResult{Matched: true, NameMatch: true, Reasons: reasons}
	}

	matchedCount := 0
	nameMatch := false
	for _, term := range query.Positive {
		alias, token, inName, hit := termMatches(term, offer)
		if hit {
			matchedCount++
			if inName {
				nameMatch = true
			}
			reasons = append(reasons, MatchReason{
				Kind:   reasonKindFor(term, alias),
				Term:   term.Original,
				Detail: explainHit(alias, token),
				InName: inName,
			})
			continue
		}
		if query.Mode == ModeAll {
			result := MatchResult{Matched: false}
			if debug {
				result.RejectedBy = []MatchReason{{
					Kind:   reasonKindFor(term, Normalize(term.Original)),
					Term:   term.Original,
					Detail: "ikke fundet",
				}}
			}
			return result
		}
	}

	if query.Mode == ModeAny && matchedCount == 0 {
		return MatchResult{Matched: false}
	}

	for _, filter := range query.Filters {
		reasons = append(reasons, MatchReason{
			Kind:   ReasonFilter,
			Term:   filter.Key(),
			Detail: filter.Explain(),
			InName: true,
		})
	}
	return MatchResult{Matched: true, NameMatch: nameMatch, Reasons: reasons}
}

// reasonKindFor classifies a term hit by how it matched: phrase terms
// always report ReasonPhrase; a hit through an alias different from the
// literal term reports ReasonConcept (term came from a concept) or
// ReasonAlias (plain alias); otherwise the hit is literal.
func reasonKindFor(term CompiledTerm, matchedAlias string) ReasonKind {
	switch {
	case term.Phrase:
		return ReasonPhrase
	case matchedAlias != Normalize(term.Original) && len(term.Concepts) > 0:
		return ReasonConcept
	case matchedAlias != Normalize(term.Original):
		return ReasonAlias
	}
	return ReasonTerm
}

// termMatches reports whether any alias of the term matches the offer's
// searchable text. The offer name is tested first so a product hit is
// never downgraded to a description hit. matchedAlias is the alias that
// hit (possibly equal to the normalized term); matchedToken is the
// compound offer token when the hit was inside a compound word; inName
// reports the hit was found in the offer name.
func termMatches(term CompiledTerm, offer PreparedOffer) (matchedAlias, matchedToken string, inName, ok bool) {
	for _, alias := range term.Aliases {
		allowCompound := !term.ExactAliases[alias]
		if token, hit := aliasMatchesIn(alias, offer.nameNorm, offer.nameCompact, offer.nameTokens, offer.nameTokenList, allowCompound); hit {
			return alias, token, true, true
		}
	}
	for _, alias := range term.Aliases {
		allowCompound := !term.ExactAliases[alias]
		if token, hit := aliasMatchesIn(alias, offer.norm, offer.compact, offer.tokens, offer.tokenList, allowCompound); hit {
			return alias, token, false, true
		}
	}
	return "", "", false, false
}

// minCompoundRunes is the normalized length at which a single-word alias
// may also match inside a compound token. The threshold was 6, but
// diacritic folding inflates short Danish words ("ørred" becomes
// "oerred", 6 runes), which made "fisk" match "tørrede abrikoser" and
// "snacks" match "kattesnacks". Seven keeps useful compounds
// ("kylling" -> "kyllingeinderfilet") without those false positives;
// real 6-rune compounds are covered by explicit aliases instead.
const minCompoundRunes = 7

// aliasMatchesIn implements the documented matching rules
// over one normalized text projection:
//
//   - single words match whole tokens of the normalized text, so "øl"
//     never matches "pølser" or "gris";
//   - longer words (see minCompoundRunes) also match inside compound
//     tokens, so "chokolade" finds "chokoladebar";
//   - multi-word aliases match as normalized substrings of the normalized
//     text, and also in space-stripped form so "chokolade bar" finds
//     "chokoladebar" and "coca-cola" comparisons stay consistent.
//
// Single-word phrases deliberately use the single-word rules: quoting a
// single word adds no phrase to match. The returned token is non-empty
// only for compound containment hits. allowCompound=false restricts the
// alias to whole-token (or phrase) matches, which is how exact aliases
// avoid ambiguous compounds.
func aliasMatchesIn(alias, norm, compactText string, tokens map[string]struct{}, tokenList []string, allowCompound bool) (token string, ok bool) {
	if alias == "" {
		return "", false
	}
	if strings.Contains(alias, " ") {
		if strings.Contains(norm, alias) {
			return "", true
		}
		joined := compact(alias)
		if joined != alias && strings.Contains(compactText, joined) {
			return "", true
		}
		return "", false
	}
	if _, found := tokens[alias]; found {
		return "", true
	}
	// Long single words may hide inside a compound token. tokenList is sorted so
	// the reported compound is stable across runs.
	if allowCompound && len([]rune(alias)) >= minCompoundRunes {
		for _, candidate := range tokenList {
			if strings.Contains(candidate, alias) {
				return candidate, true
			}
		}
	}
	return "", false
}

func quote(s string) string { return "\"" + s + "\"" }

// explainHit renders one concrete hit for MatchReason.Detail.
func explainHit(alias, token string) string {
	if token != "" {
		return quote(alias) + " ⊆ " + quote(token)
	}
	return quote(alias)
}

// FormatReasons renders match reasons as a short Danish explanation for
// previews and debug output, e.g.
// `koncept "øl" via "pilsner"; filter price:<50`.
//
// Detail is display-ready: hit details arrive pre-rendered from
// explainHit (already quoted), so they are never quoted twice.
func FormatReasons(reasons []MatchReason) string {
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		suffix := ""
		if reason.Kind != ReasonFilter && !reason.InName {
			suffix = " (i beskrivelsen)"
		}
		switch reason.Kind {
		case ReasonFilter:
			parts = append(parts, "filter "+reason.Detail)
		case ReasonConcept, ReasonAlias:
			parts = append(parts, reason.Kind.String()+" "+quote(reason.Term)+" via "+reason.Detail+suffix)
		default:
			parts = append(parts, reason.Kind.String()+" "+reason.Detail+suffix)
		}
	}
	return strings.Join(parts, "; ")
}
