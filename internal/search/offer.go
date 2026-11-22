package search

import (
	"sort"
	"strings"
)

// SearchOffer is the internal, search-oriented offer representation.
// Upstream API types are adapted into this shape at the integration
// boundary; the search layer never knows how an offer was fetched.
//
// Only fields that are actually available should be populated. Missing
// structured data never causes a match failure unless a query explicitly
// requires that field and configuration says missing data fails.
type SearchOffer struct {
	ID          string
	Name        string
	Description string
	Store       string
	Department  string

	// Price is the actual (possibly member/app) price.
	Price *Money
	// UnitPrice is the normalized unit price with its basis family.
	UnitPrice *UnitPrice
	// Discount is the discount as a percentage (permille), when known.
	Discount *Percentage
	// NormalPrice is the pre-discount reference price, when known.
	NormalPrice *Money

	// IsAppPrice marks offers only available through the retailer app.
	IsAppPrice bool

	// Attributes carries extra searchable metadata as strings.
	Attributes map[string]string
}

// PreparedOffer is an offer normalized once for evaluation against many
// compiled queries. Build it with Prepare and reuse it
// across subscriptions.
type PreparedOffer struct {
	Offer SearchOffer

	// StoreCanonical is the offer store mapped through Config.StoreAliases.
	StoreCanonical string
	// DepartmentCanonical is the department mapped through Config.DepartmentAliases.
	DepartmentCanonical string
	// Discount is the effective discount: the explicit value, or derived
	// from Price and NormalPrice when both are known and meaningful.
	Discount *Percentage

	norm    string              // all searchable text, normalized
	compact string              // norm without spaces (Danish compounds)
	tokens  map[string]struct{} // normalized word set
	// sorted lists iteration over tokens, so a compound hit reports the same
	// token (and therefore the same reason string) on every run.
	tokenList []string

	// nameNorm/nameCompact/nameTokens cover the offer name alone. A hit
	// there is a product hit; a hit only in the wider searchable text
	// (description, attributes) is weaker evidence used for ranking.
	nameNorm    string
	nameCompact string
	nameTokens  map[string]struct{}
	// nameTokenList is the sorted form of nameTokens.
	nameTokenList []string
}

// Prepare normalizes an offer's searchable text and canonicalizes its
// structured fields according to cfg. The searchable text fields are
// configurable: by default the name and description are searched, and
// attributes are included when present.
func Prepare(offer SearchOffer, cfg Config) PreparedOffer {
	parts := []string{offer.Name}
	if cfg.SearchDescription {
		parts = append(parts, offer.Description)
	}
	if cfg.SearchDepartment {
		parts = append(parts, offer.Department)
	}
	if cfg.SearchAttributes {
		for _, value := range offer.Attributes {
			parts = append(parts, value)
		}
	}
	norm := Normalize(joinNonEmpty(parts))
	tokens, tokenList := tokenSet(Tokens(norm))

	nameNorm := Normalize(offer.Name)
	nameTokens, nameTokenList := tokenSet(Tokens(nameNorm))

	prepared := PreparedOffer{
		Offer:               offer,
		StoreCanonical:      canonicalize(offer.Store, cfg.StoreAliases),
		DepartmentCanonical: canonicalize(offer.Department, cfg.DepartmentAliases),
		Discount:            effectiveDiscount(offer),
		norm:                norm,
		compact:             compact(norm),
		tokens:              tokens,
		tokenList:           tokenList,
		nameNorm:            nameNorm,
		nameCompact:         compact(nameNorm),
		nameTokens:          nameTokens,
		nameTokenList:       nameTokenList,
	}
	return prepared
}

// tokenSet builds the deduplicated token map plus a sorted token list for
// deterministic iteration.
func tokenSet(tokens []string) (map[string]struct{}, []string) {
	set := make(map[string]struct{}, len(tokens))
	list := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, seen := set[token]; seen {
			continue
		}
		set[token] = struct{}{}
		list = append(list, token)
	}
	sort.Strings(list)
	return set, list
}

// effectiveDiscount returns the explicit discount if present, otherwise
// derives it from Price and NormalPrice when the normal price is above
// the actual price.
func effectiveDiscount(offer SearchOffer) *Percentage {
	if offer.Discount != nil {
		return offer.Discount
	}
	if offer.Price == nil || offer.NormalPrice == nil {
		return nil
	}
	price, normal := int64(*offer.Price), int64(*offer.NormalPrice)
	if normal <= price || normal <= 0 {
		return nil
	}
	// permille = (normal - price) / normal * 1000, rounded half up.
	permille := ((normal-price)*1000 + normal/2) / normal
	pct := Percentage(permille)
	return &pct
}

// canonicalize maps a raw structured value through an alias table. The
// value is normalized first so "Føtex", "fotex" and "FOETEX" agree.
// Unknown values keep their normalized form (they simply never match a
// filter for another value). Alias-table values are normalized on return
// so slugs containing hyphens compare equal to their normalized offer
// side ("meat-and-fish" ≡ "meat and fish").
func canonicalize(value string, aliases map[string]string) string {
	norm := Normalize(value)
	if norm == "" {
		return ""
	}
	if aliases != nil {
		if canonical, ok := aliases[norm]; ok {
			return Normalize(canonical)
		}
		// Raw (lowercased) lookup so hyphenated slug keys stay reachable,
		// since normalization turns hyphens into spaces.
		if canonical, ok := aliases[strings.ToLower(strings.TrimSpace(value))]; ok {
			return Normalize(canonical)
		}
	}
	return norm
}

func joinNonEmpty(parts []string) string {
	var builder strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(part)
	}
	return builder.String()
}
