package match

import (
	"context"
	"testing"

	"tjekapi"
)

func testHotspots() []tjekapi.Hotspot {
	return []tjekapi.Hotspot{
		{OfferName: "Pepsi Max 18x33 cl, DKK 59", OfferID: "pepsi"},
		{OfferName: "Kims Chips Sour Cream, DKK 18", OfferID: "kims"},
		{OfferName: "Faxe Kondi 6x33 cl, DKK 25", OfferID: "faxe"},
		{OfferName: "Marabou 200 g, DKK 15", OfferID: "marabou"},
		{OfferName: "Rugbrød, DKK 12", OfferID: "bread"},
		{OfferName: "Haribo Matador Mix, DKK 20", OfferID: "haribo"},
		{OfferName: "Smirnoff Ice 70 cl, DKK 89", OfferID: "smirnoff"},
		{OfferName: "Tuborg Grøn 30x33 cl, DKK 149", OfferID: "tuborg"},
	}
}

func matchedIDs(t *testing.T, scored []ScoredDeal) map[string]ScoredDeal {
	t.Helper()
	out := make(map[string]ScoredDeal, len(scored))
	for _, sd := range scored {
		out[sd.Deal.OfferID] = sd
	}
	return out
}

func TestStrictMatcherGoldenCases(t *testing.T) {
	wants := []Want{
		{ID: "chips", Include: []string{"chips"}},
		{ID: "chokolade", Include: []string{"marabou"}},
		{ID: "lakrids-fri", Include: []string{"haribo"}, Exclude: []string{"lakrids"}},
		{ID: "smirnoff", Include: []string{`"smirnoff ice"`}},
		{ID: "billig", Include: []string{"pepsi", "kims", "rugbrød"}, MaxPrice: floatPtr(20)},
	}

	scored := KeywordMatcher{}.Match(context.Background(), testHotspots(), wants)
	got := matchedIDs(t, scored)

	// Strict means no concept expansion: bare brand words match nothing by
	// themselves. Pepsi, Faxe and Tuborg carry no wanted token, so they
	// stay out even though a "sodavand"/"øl" category would have claimed them.
	for _, id := range []string{"pepsi", "faxe", "tuborg"} {
		if sd, ok := got[id]; ok {
			t.Errorf("strict: %q must not match, got (%q, %.2f, %s)", id, sd.WantID, sd.Score, sd.Reason)
		}
	}

	// Exact token still hits for "chips" on kims.
	if sd, ok := got["kims"]; !ok || sd.WantID != "chips" || sd.Score != ScoreExact {
		t.Errorf("want chips on kims = %+v, want exact hit", sd)
	}

	// Exclusion vetoes lakrids-free haribo... but Matador Mix has no lakrids, so it matches.
	if sd, ok := got["haribo"]; !ok || sd.WantID != "lakrids-fri" {
		t.Errorf("want lakrids-fri on haribo = %+v, want match (no lakrids present)", sd)
	}

	// Quoted phrase matches Smirnoff Ice.
	if sd, ok := got["smirnoff"]; !ok || sd.Score != ScoreExact {
		t.Errorf("want smirnoff on smirnoff = %+v, want phrase hit", sd)
	}

	// MaxPrice 20 keeps rugbrød (12 kr). Kims is already claimed by the
	// earlier "chips" want; pepsi (59 kr) matches nothing.
	if sd, ok := got["bread"]; !ok || sd.WantID != "billig" {
		t.Errorf("want billig on bread = %+v, want match", sd)
	}
}

// TestStrictMatcherRejectsReportedFalsePositives pins the two production
// misses: tortilla wraps are not chips, Premier Is is not Smirnoff Ice.
func TestStrictMatcherRejectsReportedFalsePositives(t *testing.T) {
	hotspots := []tjekapi.Hotspot{
		{OfferName: "Tortilla wraps fuldkorn, DKK 8", OfferID: "tortilla-wraps"},
		{OfferName: "Premier Is isbæger eller iskasse, DKK 32", OfferID: "premier-is"},
		{OfferName: "Kims Chips Sour Cream, DKK 18", OfferID: "kims"},
		{OfferName: "Smirnoff Ice 70 cl, DKK 89", OfferID: "smirnoff"},
	}
	wants := []Want{
		{ID: "chips", Include: []string{"chips"}},
		{ID: "smirnoff ice", Include: []string{"smirnoff ice"}},
	}

	for _, matcher := range []Matcher{KeywordMatcher{}, ForName(MatcherNormalized), ForName(MatcherSemantic)} {
		scored := matcher.Match(context.Background(), hotspots, wants)
		got := matchedIDs(t, scored)
		if sd, ok := got["tortilla-wraps"]; ok {
			t.Errorf("%s: tortilla wraps must not match, got (%q, %.2f, %s)",
				matcher.Name(), sd.WantID, sd.Score, sd.Reason)
		}
		if sd, ok := got["premier-is"]; ok {
			t.Errorf("%s: premier Is must not match, got (%q, %.2f, %s)",
				matcher.Name(), sd.WantID, sd.Score, sd.Reason)
		}
		if _, ok := got["kims"]; !ok {
			t.Errorf("%s: Kims Chips must still match chips", matcher.Name())
		}
		if _, ok := got["smirnoff"]; !ok {
			t.Errorf("%s: Smirnoff Ice must still match", matcher.Name())
		}
	}
}

func TestStrictMatcherDanishFoldingAndCompounds(t *testing.T) {
	hotspots := []tjekapi.Hotspot{
		{OfferName: "Snøfler, DKK 10", OfferID: "snøfler"},
		{OfferName: "Æbleskiver 400 g, DKK 22", OfferID: "æble"},
		{OfferName: "Chokoladebar, DKK 8", OfferID: "bar"},
	}
	wants := []Want{
		{ID: "fold", Include: []string{"snoefler", "aebleskiver", "chokolade bar"}},
	}

	scored := KeywordMatcher{}.Match(context.Background(), hotspots, wants)
	got := matchedIDs(t, scored)

	// ASCII-folded spellings match Danish names.
	for _, id := range []string{"snøfler", "æble"} {
		if _, ok := got[id]; !ok {
			t.Errorf("folded spelling: expected match on %q", id)
		}
	}
	// Compound splitting: "chokolade bar" matches "Chokoladebar".
	if _, ok := got["bar"]; !ok {
		t.Error(`compound split: "chokolade bar" should match "Chokoladebar"`)
	}
}

func TestStrictMatcherIgnoresTypos(t *testing.T) {
	// Typo tolerance was a fuzzy-matching false-positive source: "doritos"
	// must not match "Dorotos". Spell it right or add the variant.
	hotspots := []tjekapi.Hotspot{
		{OfferName: "Dorotos 150 g, DKK 15", OfferID: "typo"},
	}
	wants := []Want{{ID: "chips", Include: []string{"doritos"}}}

	scored := KeywordMatcher{}.Match(context.Background(), hotspots, wants)
	if len(scored) != 0 {
		t.Errorf("strict: typo must not match, got %+v", scored)
	}
}

func TestStrictMatcherIgnoresDanishVowelVariants(t *testing.T) {
	// "monster" (the energy drink) must not match "mønster" ("pattern"):
	// the 1-edit difference is the "e" that ø folds to, not a typo.
	// Strict matching never had this problem; the test pins it.
	hotspots := []tjekapi.Hotspot{
		{OfferName: "Sweater med mønster, DKK 199", OfferID: "sweater"},
		{OfferName: "Monster Energy 4x50 cl, DKK 45", OfferID: "monster-drink"},
	}
	wants := []Want{{ID: "monster", Include: []string{"monster"}}}

	scored := KeywordMatcher{}.Match(context.Background(), hotspots, wants)
	got := matchedIDs(t, scored)
	if sd, ok := got["sweater"]; ok {
		t.Errorf("strict: monster must not match mønster (pattern), got %+v", sd)
	}
	if _, ok := got["monster-drink"]; !ok {
		t.Error("exact: monster must still match Monster Energy")
	}
}

func TestStrictMatcherExclusionAndAppPrice(t *testing.T) {
	hotspots := []tjekapi.Hotspot{
		{OfferName: "Haribo Lakrids, DKK 20", OfferID: "lakrids"},
		{OfferName: "Kims Chips, DKK 18, App-pris", OfferID: "app"},
	}
	allowApp := false
	wants := []Want{
		{ID: "no-lakrids", Include: []string{"haribo"}, Exclude: []string{"lakrids"}},
		{ID: "no-app", Include: []string{"kims"}, AllowAppPrice: &allowApp},
		{ID: "app-ok", Include: []string{"kims"}},
	}

	scored := KeywordMatcher{}.Match(context.Background(), hotspots, wants)
	got := matchedIDs(t, scored)

	if _, ok := got["lakrids"]; ok {
		t.Error("exclusion: Haribo Lakrids must be vetoed by exclude lakrids")
	}
	if sd, ok := got["app"]; !ok || sd.WantID != "app-ok" {
		t.Errorf("app-price: expected match only via app-ok, got %+v", sd)
	}
}

func TestStrictMatcherNoAliasExpansion(t *testing.T) {
	// "faxe kondi" is a phrase: it matches "Faxe Kondi", not a lone "Faxe".
	// The old alias table claimed the short form; strict does not.
	hotspots := []tjekapi.Hotspot{
		{OfferName: "Faxe 6x33 cl, DKK 25", OfferID: "faxe-short"},
		{OfferName: "Faxe Kondi 6x33 cl, DKK 25", OfferID: "faxe-full"},
	}
	wants := []Want{{ID: "faxe", Include: []string{"faxe kondi"}}}

	scored := KeywordMatcher{}.Match(context.Background(), hotspots, wants)
	got := matchedIDs(t, scored)
	if _, ok := got["faxe-short"]; ok {
		t.Errorf("strict: phrase must not match the short form, got %+v", got["faxe-short"])
	}
	if _, ok := got["faxe-full"]; !ok {
		t.Error("strict: phrase must still match the full name")
	}
}

func TestKeywordMatcherPreservesLegacyBehavior(t *testing.T) {
	wants := WantsFromKeywords([]string{"chips"})
	scored := KeywordMatcher{}.Match(context.Background(), testHotspots(), wants)
	if len(scored) != 1 || scored[0].Deal.OfferID != "kims" {
		t.Fatalf("strict keyword match = %+v, want the kims deal", scored)
	}
	if scored[0].WantID != "chips" || scored[0].Score != ScoreExact {
		t.Errorf("strict attribution = %+v, want WantID chips score 1.0", scored[0])
	}
}

func TestWantsFromKeywordsAndRetailerFilter(t *testing.T) {
	wants := WantsFromKeywords([]string{"pepsi", "", "chips"})
	if len(wants) != 2 || wants[0].ID != "pepsi" || len(wants[0].Include) != 1 {
		t.Fatalf("WantsFromKeywords = %+v", wants)
	}

	wants[0].Retailers = []string{"spar"}
	filtered := FilterWantsByRetailer(wants, "fotex")
	if len(filtered) != 1 || filtered[0].ID != "chips" {
		t.Fatalf("FilterWantsByRetailer(fotex) = %+v", filtered)
	}
	if got := FilterWantsByRetailer(wants, "SPAR"); len(got) != 2 {
		t.Fatalf("FilterWantsByRetailer(SPAR) = %+v, want both", got)
	}
}

func TestMatchDealReResolvesAfterDedup(t *testing.T) {
	deal := tjekapi.Deal{OfferName: "Pepsi Max, DKK 59", CleanName: "Pepsi Max"}
	price := 59.0
	deal.Price = &price
	wants := []Want{{ID: "pepsi", Include: []string{"pepsi"}}}

	sd, ok := MatchDeal(deal, wants)
	if !ok || sd.WantID != "pepsi" {
		t.Fatalf("MatchDeal = %+v, %v; want pepsi hit", sd, ok)
	}
	if len(sd.Deal.MatchingKeywords) == 0 {
		t.Error("MatchDeal should record the matched term")
	}
}

func TestForNameMatcherSelection(t *testing.T) {
	if DefaultMatcher != MatcherQuery {
		t.Errorf("DefaultMatcher = %q, want %q", DefaultMatcher, MatcherQuery)
	}
	if got := ForName("keywords").Name(); got != MatcherKeywords {
		t.Errorf("ForName(keywords) = %q", got)
	}
	if got := ForName("query").Name(); got != MatcherQuery {
		t.Errorf("ForName(query) = %q", got)
	}
	// "normalized" and "semantic" are deprecated aliases resolved to the strict
	// keyword matcher, so configs written for the removed matchers keep posting
	// the same results instead of silently switching engines.
	for _, name := range []string{"normalized", "semantic"} {
		if got := ForName(name).Name(); got != MatcherKeywords {
			t.Errorf("ForName(%q) = %q, want %q", name, got, MatcherKeywords)
		}
	}
	// Unknown and empty names follow the default matcher.
	for _, name := range []string{"bogus", ""} {
		if got := ForName(name).Name(); got != MatcherQuery {
			t.Errorf("ForName(%q) = %q, want %q", name, got, MatcherQuery)
		}
	}
}

func floatPtr(f float64) *float64 { return &f }
