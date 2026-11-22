package service

import (
	"testing"

	"spar-tilbuds-bot/internal/match"

	"tjekapi"
)

func TestCachedVerdictTOMLRoundTrip(t *testing.T) {
	want := cachedVerdict{
		ProductName: `Carlsberg "Classic"`,
		ProductURL:  "https://example.test/product",
		Verdict: tjekapi.PrejOfferVerdict{
			CandidateOre: 1250, CandidateDKK: 12.5, CheapestOre: 1100,
			CheapestChain: "SPAR", MinHistoryOre: 900, PctAboveCheapest: 13.64,
			PctAboveLowest: 38.89, IsCheapestNow: false, IsLowestEver: true, IsGoodDeal: true,
		},
	}
	got, err := parseCachedVerdictTOML(cachedVerdictTOML(want))
	if err != nil {
		t.Fatalf("parseCachedVerdictTOML() error = %v", err)
	}
	if got != want {
		t.Fatalf("TOML round trip = %+v, want %+v", got, want)
	}
}

func TestDiscountSignal(t *testing.T) {
	if got := discountSignal(tjekapi.PrejOfferVerdict{}); got != 1.0 {
		t.Errorf("empty verdict = %v, want neutral 1.0", got)
	}
	if got := discountSignal(tjekapi.PrejOfferVerdict{IsLowestEver: true, PctAboveLowest: 0}); got != 1.0 {
		t.Errorf("lowest ever = %v, want 1.0", got)
	}
	if got := discountSignal(tjekapi.PrejOfferVerdict{PctAboveLowest: 10}); got != 0.9 {
		t.Errorf("10pct above = %v, want 0.9", got)
	}
	if got := discountSignal(tjekapi.PrejOfferVerdict{PctAboveLowest: 80}); got != 0.5 {
		t.Errorf("80pct above = %v, want floored 0.5", got)
	}
	if got := discountSignal(tjekapi.PrejOfferVerdict{PctAboveLowest: 30, IsGoodDeal: true}); got != 0.9 {
		t.Errorf("good deal lift = %v, want 0.9", got)
	}
}

func TestPrejBadges(t *testing.T) {
	if badges := prejBadges(tjekapi.PrejOfferVerdict{}); len(badges) != 0 {
		t.Errorf("empty verdict badges = %v, want none", badges)
	}
	badges := prejBadges(tjekapi.PrejOfferVerdict{IsLowestEver: true, IsGoodDeal: true})
	if len(badges) != 1 || badges[0] != "billigste i 30 dage" {
		t.Errorf("lowest ever badges = %v", badges)
	}
	badges = prejBadges(tjekapi.PrejOfferVerdict{IsCheapestNow: true})
	if len(badges) != 1 || badges[0] != "billigste lige nu" {
		t.Errorf("cheapest now badges = %v", badges)
	}
}

func TestPickPrejResult(t *testing.T) {
	results := []tjekapi.PrejSearchResult{
		{URL: "u1", Name: "Arla Letmælk 1L", MinPriceDKK: 12},
		{URL: "u2", Name: "Kims Chips Sour Cream 170g", MinPriceDKK: 18, OnOffer: true},
		{URL: "u3", Name: "Pepsi Max 33cl", MinPriceDKK: 10},
	}
	best, ok := pickPrejResult("Kims Chips Sour Cream, DKK 18", results)
	if !ok || best.URL != "u2" {
		t.Errorf("pick = %+v %v, want u2", best, ok)
	}
	if _, ok := pickPrejResult("Ukendt dimsedut 999", results); ok {
		t.Error("unrelated deal must not match any product")
	}
	if _, ok := pickPrejResult("", results); ok {
		t.Error("empty name must not match")
	}
}

func TestRankDealsCapsAndSorts(t *testing.T) {
	deals := []match.ScoredDeal{
		{WantID: "a", Score: 0.5, Final: 0.4},
		{WantID: "b", Score: 0.9, Final: 0.9},
		{WantID: "c", Score: 0.7},
	}
	ranked := rankDeals(deals, 2)
	if len(ranked) != 2 || ranked[0].WantID != "b" || ranked[1].WantID != "c" {
		t.Errorf("ranked = %+v, want b then c", ranked)
	}
	// Unranked deals order by Score through RankScore.
	unranked := rankDeals([]match.ScoredDeal{{WantID: "x", Score: 0.3}, {WantID: "y", Score: 0.8}}, 0)
	if unranked[0].WantID != "y" {
		t.Errorf("unranked order = %+v, want y first", unranked)
	}
}

func TestTjekBusinessName(t *testing.T) {
	for _, retailer := range []string{"", "spar", "SPAR"} {
		if got, err := tjekBusinessName(retailer); err != nil || got != "SPAR" {
			t.Errorf("retailer %q = %q %v", retailer, got, err)
		}
	}
	if got, err := tjekBusinessName("fotex"); err != nil || got != "Føtex" {
		t.Errorf("fotex = %q %v", got, err)
	}
	if got, err := tjekBusinessName("lidl"); err != nil || got != "Lidl" {
		t.Errorf("lidl = %q %v", got, err)
	}
	if _, err := tjekBusinessName("netto"); err == nil {
		t.Error("netto must be rejected (only spar+fotex+lidl supported)")
	}
}

func TestRetailersListsEverySupportedFeed(t *testing.T) {
	want := []string{RetailerSPAR, RetailerFotex, RetailerLidl}
	got := Retailers()
	if len(got) != len(want) {
		t.Fatalf("Retailers() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Retailers() = %v, want %v", got, want)
		}
	}
}

func TestNormKeySeparatesPackSizes(t *testing.T) {
	plain := match.NormKey("Pepsi Max", 0, 0)
	pack18 := match.NormKey("Pepsi Max", 18, 33)
	pack6 := match.NormKey("Pepsi Max", 6, 33)
	if plain == "" || pack18 == plain || pack18 == pack6 {
		t.Errorf("keys must differ: %q %q %q", plain, pack18, pack6)
	}
}
func TestResolvePriorDealKeepsSemanticScore(t *testing.T) {
	price := 25.0
	deal := tjekapi.Deal{OfferName: "Cola uden sukker 6x33cl", CleanName: "Cola uden sukker", Price: &price, OfferID: "cola", MatchingKeywords: []string{"sodavand cola pepsi"}}
	prior := map[string]match.ScoredDeal{
		"cola": {Deal: deal, WantID: "sodavand", Score: 0.62, Reason: `semantisk "sodavand" (0.62)`},
	}
	wants := []match.Want{{ID: "sodavand", Include: []string{"sodavand", "cola", "pepsi"}}}
	sd, ok := resolvePriorDeal(wants, prior, deal)
	if !ok {
		t.Fatal("prior semantic hit must survive enrichment")
	}
	if sd.WantID != "sodavand" || sd.Score != 0.62 || sd.Reason == "" {
		t.Errorf("carried score = %+v, want sodavand/0.62 with reason", sd)
	}
}

func TestResolvePriorDealFallsBackOnGuard(t *testing.T) {
	price := 10.0
	maxPrice := 1.0
	deal := tjekapi.Deal{OfferName: "Cola", CleanName: "Cola", Price: &price, OfferID: "cola", MatchingKeywords: []string{"cola"}}
	prior := map[string]match.ScoredDeal{
		"cola": {Deal: deal, WantID: "strict", Score: 1.0, Reason: `ord "cola"`},
	}
	wants := []match.Want{
		{ID: "strict", Include: []string{"cola"}, MaxPrice: &maxPrice},
		{ID: "loose", Include: []string{"cola"}},
	}
	resolved := resolveDeals(wants, prior, []tjekapi.Deal{deal})
	if len(resolved) != 1 || resolved[0].WantID != "loose" {
		t.Errorf("resolved = %+v, want the later loose want", resolved)
	}
}

func TestPrejGroupKeyIncludesPrice(t *testing.T) {
	a := match.ScoredDeal{Deal: tjekapi.Deal{CleanName: "Pepsi Max", Price: floatPtr(10)}}
	b := match.ScoredDeal{Deal: tjekapi.Deal{CleanName: "Pepsi Max", Price: floatPtr(12)}}
	keyA, okA := prejGroupKey("spar", a)
	keyB, okB := prejGroupKey("spar", b)
	if !okA || !okB || keyA == keyB {
		t.Errorf("group keys = %q/%q, want distinct keys per price", keyA, keyB)
	}
	noPrice := match.ScoredDeal{Deal: tjekapi.Deal{CleanName: "Pepsi Max"}}
	if _, ok := prejGroupKey("spar", noPrice); ok {
		t.Error("priceless deal must not group")
	}
}

func floatPtr(f float64) *float64 { return &f }
