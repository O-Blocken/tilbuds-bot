package search

import (
	"strings"
	"testing"
)

// offer builds a prepared offer with optional field modifiers.
func offer(name string, mods ...func(*SearchOffer)) PreparedOffer {
	o := SearchOffer{Name: name}
	for _, mod := range mods {
		mod(&o)
	}
	return Prepare(o, DefaultConfig())
}

func withPrice(ore int64) func(*SearchOffer) {
	return func(o *SearchOffer) { m := Money(ore); o.Price = &m }
}

func withDescription(text string) func(*SearchOffer) {
	return func(o *SearchOffer) { o.Description = text }
}

func withNormalPrice(ore int64) func(*SearchOffer) {
	return func(o *SearchOffer) { m := Money(ore); o.NormalPrice = &m }
}

func withDiscount(permille int64) func(*SearchOffer) {
	return func(o *SearchOffer) { p := Percentage(permille); o.Discount = &p }
}

func withUnit(ore int64, family UnitFamily) func(*SearchOffer) {
	return func(o *SearchOffer) {
		o.UnitPrice = &UnitPrice{OrePerBasis: ore, Family: family}
	}
}

func withStore(store string) func(*SearchOffer) {
	return func(o *SearchOffer) { o.Store = store }
}

func withDept(dept string) func(*SearchOffer) {
	return func(o *SearchOffer) { o.Department = dept }
}

func withAppPrice() func(*SearchOffer) {
	return func(o *SearchOffer) { o.IsAppPrice = true }
}

// mustCompile compiles a raw query against the default registry.
func mustCompile(t *testing.T, raw string, cfg Config) CompiledQuery {
	t.Helper()
	q, err := ParseQueryWithConfig(raw, cfg)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", raw, err)
	}
	cq, err := CompileQuery(q, registry(t), cfg)
	if err != nil {
		t.Fatalf("CompileQuery(%q): %v", raw, err)
	}
	return cq
}

// TestRequiredBehavioralCases covers the documented query behaviour.
func TestRequiredBehavioralCases(t *testing.T) {
	tests := []struct {
		name  string
		query string
		offer PreparedOffer
		want  bool
	}{
		{
			name:  "beer matches, not alcohol-free, not advent calendar",
			query: "øl -alkoholfri -julekalender",
			offer: offer("Carlsberg Pilsner 6x33cl"),
			want:  true,
		},
		{
			name:  "alcohol-free beer is rejected despite positive match",
			query: "øl -alkoholfri -julekalender",
			offer: offer("Carlsberg Alkoholfri 6x33cl"),
			want:  false,
		},
		{
			name:  "beer advent calendar is rejected despite positive match",
			query: "øl -julekalender",
			offer: offer("Øl Julekalender 24x33cl"),
			want:  false,
		},
		{
			name:  "regular coca cola phrase matches",
			query: `"coca cola" -zero`,
			offer: offer("Coca Cola Sodavand 1,5l"),
			want:  true,
		},
		{
			name:  "coca cola zero is rejected",
			query: `"coca cola" -zero`,
			offer: offer("Coca-Cola Zero 1,5l"),
			want:  false,
		},
		{
			name:  "concept matches through configured alias without literal word",
			query: "øl",
			offer: offer("Carlsberg 6x33cl"),
			want:  true,
		},
		{
			name:  "excluded concept rejects through any configured alias",
			query: "flasker -øl",
			offer: offer("Carlsberg flasker"),
			want:  false,
		},
		{
			name:  "exclusion does not fire without alias hit",
			query: "flasker -øl",
			offer: offer("Faxe Kondi flasker"),
			want:  true,
		},
		{
			name:  "price below threshold matches",
			query: "price:<50",
			offer: offer("Uden navn", withPrice(4995)),
			want:  true,
		},
		{
			name:  "price at threshold rejected by strict <",
			query: "price:<50",
			offer: offer("Uden navn", withPrice(5000)),
			want:  false,
		},
		{
			name:  "price at threshold matched by <=",
			query: "price:<=50",
			offer: offer("Uden navn", withPrice(5000)),
			want:  true,
		},
		{
			name:  "missing price fails an explicit price filter",
			query: "price:<50",
			offer: offer("Uden pris"),
			want:  false,
		},
		{
			name:  "unit price in an incompatible family is never compared",
			query: "unitprice:kg:<80",
			offer: offer("Mælk", withUnit(1000, UnitVolume)),
			want:  false,
		},
		{
			name:  "unit price in a compatible family compares",
			query: "unitprice:kg:<80",
			offer: offer("Hakket oksekød", withUnit(7999, UnitMass)),
			want:  true,
		},
		{
			name:  "discount exactly 25 percent matches >=",
			query: "discount:>=25%",
			offer: offer("Tilbud", withDiscount(250)),
			want:  true,
		},
		{
			name:  "discount below 25 percent is rejected",
			query: "discount:>=25%",
			offer: offer("Tilbud", withDiscount(245)),
			want:  false,
		},
		{
			name:  "and semantics: both words present",
			query: "hakket oksekød",
			offer: offer("Hakket Oksekød 8-12%"),
			want:  true,
		},
		{
			name:  "and semantics: one word missing rejects",
			query: "hakket oksekød",
			offer: offer("Hakket Kylling"),
			want:  false,
		},
	}

	cfg := DefaultConfig()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiled := mustCompile(t, tt.query, cfg)
			if got := Match(compiled, tt.offer); got.Matched != tt.want {
				t.Fatalf("Match(%q, %q) = %v, want %v", tt.query, tt.offer.Offer.Name, got.Matched, tt.want)
			}
		})
	}
}

func TestMissingPriceConfigurable(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MissingFieldFails = false
	compiled := mustCompile(t, "price:<50", cfg)
	if result := Match(compiled, offer("Uden pris")); !result.Matched {
		t.Fatal("legacy config must let offers with unknown price pass")
	}

	strict := mustCompile(t, "price:<50", DefaultConfig())
	if result := Match(strict, offer("Uden pris")); result.Matched {
		t.Fatal("default config must reject offers with unknown price")
	}
}

func TestFiltersOnlyQuery(t *testing.T) {
	compiled := mustCompile(t, "price:<50", DefaultConfig())
	if result := Match(compiled, offer("Alt billigt", withPrice(1000))); !result.Matched {
		t.Fatal("filters-only query must match every surviving offer")
	}
	if result := Match(compiled, offer("Alt dyrt", withPrice(9000))); result.Matched {
		t.Fatal("filters-only query must still apply its filters")
	}
}

func TestModeAny(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeAny
	compiled := mustCompile(t, "cola fanta", cfg)
	if result := Match(compiled, offer("Fanta appelsin")); !result.Matched {
		t.Fatal("ModeAny must match when one term matches")
	}
	if result := Match(compiled, offer("Rødvin")); result.Matched {
		t.Fatal("ModeAny must reject when no term matches")
	}
}

func TestExclusionOverridesPositiveMatch(t *testing.T) {
	// The offer satisfies the positive concept through an alias AND the
	// excluded term is present: the exclusion must win.
	compiled := mustCompile(t, "øl -alkoholfri", DefaultConfig())
	offerUnderTest := offer("Tuborg Classic Alkoholfri", withPrice(1000))
	result := MatchDebug(compiled, offerUnderTest)
	if result.Matched {
		t.Fatal("exclusion must override a positive concept match")
	}
	if len(result.RejectedBy) == 0 {
		t.Fatal("debug mode must identify the rejection cause")
	}
	if result.RejectedBy[0].Term != "alkoholfri" {
		t.Fatalf("rejected by %q, want alkoholfri", result.RejectedBy[0].Term)
	}
}

func TestMatchReasonsExplainHits(t *testing.T) {
	compiled := mustCompile(t, `øl "coca cola" max:50`, DefaultConfig())
	offerUnderTest := offer("Carlsberg og Coca Cola", withPrice(4000))
	result := Match(compiled, offerUnderTest)
	if !result.Matched {
		t.Fatal("expected match")
	}
	if len(result.Reasons) != 3 {
		t.Fatalf("want 3 reasons (2 terms + 1 filter), got %d: %+v", len(result.Reasons), result.Reasons)
	}
	formatted := FormatReasons(result.Reasons)
	if formatted == "" {
		t.Fatal("FormatReasons must render the reasons")
	}
	t.Logf("reasons: %s", formatted)

	// Concept hit should be attributed to the concept, not the literal.
	found := false
	for _, reason := range result.Reasons {
		if reason.Kind == ReasonConcept && reason.Term == "øl" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a ReasonConcept for øl, got %+v", result.Reasons)
	}
}

func TestDebugString(t *testing.T) {
	compiled := mustCompile(t, `øl -alkoholfri price:<100`, DefaultConfig())
	debug := compiled.DebugString()
	for _, want := range []string{"øl", "alkoholfri", "price:<100", "mode=all"} {
		if !strings.Contains(debug, want) {
			t.Fatalf("DebugString missing %q:\n%s", want, debug)
		}
	}
}

func TestShortWordsNeverMatchCompounds(t *testing.T) {
	// The classic traps: "øl" must not match "pølser", "is" must not
	// match "gris", and quoting a single word must not turn it into a
	// substring search ("poelser" contains "oel").
	for _, q := range []string{"is", `"is"`} {
		compiled := mustCompile(t, q, DefaultConfig())
		if Match(compiled, offer("Gris i fad")).Matched {
			t.Fatalf("%s matched gris", q)
		}
	}
	for _, q := range []string{"øl", `"øl"`} {
		compiled := mustCompile(t, q, DefaultConfig())
		if Match(compiled, offer("Pølser")).Matched {
			t.Fatalf("%s matched pølser", q)
		}
	}
}

func TestCompoundMatchingAvoidsFoldedFalsePositives(t *testing.T) {
	// "ørred" folds to "oerred" (6 runes) and used to match inside
	// "tørrede" ("toerrede"): fish must not match dried fruit. "snacks"
	// (6 runes) used to match "kattesnacks" (pet food).
	fisk := mustCompile(t, "fisk", DefaultConfig())
	if Match(fisk, offer("Tørrede abrikoser")).Matched {
		t.Fatal("fisk must not match 'tørrede' through the ørred alias")
	}
	snacks := mustCompile(t, "snacks", DefaultConfig())
	if Match(snacks, offer("Kattesnacks")).Matched {
		t.Fatal("snacks must not match pet food compounds")
	}

	// Real compounds above the threshold must keep matching.
	kylling := mustCompile(t, "kylling", DefaultConfig())
	for _, name := range []string{"Kyllingeinderfilet", "Tandoori-kyllingespyd"} {
		if result := Match(kylling, offer(name)); !result.Matched {
			t.Fatalf("kylling must match %q", name)
		}
	}
}

func TestExactAliasesDoNotMatchCompounds(t *testing.T) {
	// "pålæg" is marked exact: it must match cold cuts, not chocolate
	// spread or a slicing machine (both real Lidl offer names).
	paalaeg := mustCompile(t, "pålæg", DefaultConfig())
	if !Match(paalaeg, offer("DULANO Pålæg")).Matched {
		t.Fatal("pålæg must match a cold-cut offer")
	}
	if !Match(paalaeg, offer("Riberhus Pålægsost")).Matched {
		t.Fatal("pålæg must still match its explicit compound alias pålægsost")
	}
	for _, name := range []string{"CARLETTI Chokopålæg", "SILVERCREST Elektrisk pålægsmaskine"} {
		if Match(paalaeg, offer(name)).Matched {
			t.Fatalf("exact alias pålæg must not match %q", name)
		}
	}

	// "nødder" is exact, so Christmas cookies (pebernødder) are not nuts.
	nuts := mustCompile(t, "nødder", DefaultConfig())
	if Match(nuts, offer("Pebernødder")).Matched {
		t.Fatal("nødder must not match pebernødder")
	}
	if !Match(nuts, offer("Cashewnødder")).Matched {
		t.Fatal("nødder must match real nut offers through aliases")
	}
}

func TestDescriptionOnlyHitIsNotANameMatch(t *testing.T) {
	cfg := DefaultConfig()
	compiled := mustCompile(t, "ost", cfg)
	result := Match(compiled, offer("SILVERCREST Elektrisk pålægsmaskine", withDescription("Ost og pålæg skæres i skiver")))
	if !result.Matched {
		t.Fatal("description text is searchable by default")
	}
	if result.NameMatch {
		t.Fatal("a hit only in the description must not count as a name match")
	}
	formatted := FormatReasons(result.Reasons)
	if !strings.Contains(formatted, "i beskrivelsen") {
		t.Fatalf("reason must mark description hits: %s", formatted)
	}

	nameHit := Match(compiled, offer("Puck ost"))
	if !nameHit.Matched || !nameHit.NameMatch {
		t.Fatalf("a product-name hit must set NameMatch: %+v", nameHit)
	}
}

func TestDerivedDiscountFromNormalPrice(t *testing.T) {
	// 80 -> 60 kr is exactly 25% off.
	compiled := mustCompile(t, "discount:>=25%", DefaultConfig())
	derived := offer("Tilbud", withPrice(6000), withNormalPrice(8000))
	if result := Match(compiled, derived); !result.Matched {
		t.Fatalf("derived discount should be 25%%: %+v", derived.Discount)
	}
	// Explicit discount wins over derivation.
	explicit := offer("Tilbud", withPrice(6000), withNormalPrice(8000), withDiscount(100))
	if result := Match(compiled, explicit); result.Matched {
		t.Fatal("explicit discount must not be overridden by derivation")
	}
}

func TestStoreAndDeptAndAppFilters(t *testing.T) {
	cfg := DefaultConfig()

	compiled := mustCompile(t, "cola store:føtex", cfg)
	if result := Match(compiled, offer("Coca Cola", withStore("fotex"))); !result.Matched {
		t.Fatal("store:føtex must match a føtex offer through alias canonicalization")
	}
	if result := Match(compiled, offer("Coca Cola", withStore("spar"))); result.Matched {
		t.Fatal("store:føtex must not match a spar offer")
	}

	compiled = mustCompile(t, "cola store:lidl", cfg)
	if result := Match(compiled, offer("Coca Cola", withStore("lidl"))); !result.Matched {
		t.Fatal("store:lidl must match a Lidl offer")
	}
	if result := Match(compiled, offer("Coca Cola", withStore("fotex"))); result.Matched {
		t.Fatal("store:lidl must not match a føtex offer")
	}

	compiled = mustCompile(t, "oksebøf dept:kød", cfg)
	if result := Match(compiled, offer("Oksebøf", withDept("meat-and-fish"))); !result.Matched {
		t.Fatal("dept:kød must resolve to the meat-and-fish slug")
	}
	if result := Match(compiled, offer("Chips", withDept("snacks-and-candies"))); result.Matched {
		t.Fatal("dept:kød must not match another department")
	}

	compiled = mustCompile(t, "cola app:false", cfg)
	if result := Match(compiled, offer("Coca Cola")); !result.Matched {
		t.Fatal("app:false must match regular offers")
	}
	if result := Match(compiled, offer("Coca Cola", withAppPrice())); result.Matched {
		t.Fatal("app:false must reject app-price offers")
	}
}
