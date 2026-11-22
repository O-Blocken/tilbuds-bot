package search

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// realOffer mirrors one offer in the TOML fixture captured by cmd/dealfetch.
type realOffer struct {
	Retailer    string
	OfferName   string
	CleanName   string
	Price       *float64
	IsAppPrice  bool
	UnitPrice   *float64
	BaseUnit    string
	NormalPrice *float64
	RelSavings  *float64
	Description string
	Department  string
}

type realFixture struct {
	FetchedAt string
	Offers    []realOffer
}

// loadRealOffers reads the captured publication fixture. The fixture is
// refreshed with: go run ./cmd/dealfetch -out internal/search/testdata/real_offers.toml
func loadRealOffers(t *testing.T) realFixture {
	t.Helper()
	file, err := os.Open("testdata/real_offers.toml")
	if err != nil {
		t.Skipf("real-offer fixture missing (run cmd/dealfetch): %v", err)
	}
	defer file.Close()
	var fixture realFixture
	var current *realOffer
	scanner := bufio.NewScanner(file)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[[offers]]" {
			fixture.Offers = append(fixture.Offers, realOffer{})
			current = &fixture.Offers[len(fixture.Offers)-1]
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("fixture line %d: expected key=value", lineNo)
		}
		key, raw = strings.TrimSpace(key), strings.TrimSpace(raw)
		if key == "fetched_at" {
			fixture.FetchedAt, err = strconv.Unquote(raw)
			if err != nil {
				t.Fatalf("fixture line %d: %v", lineNo, err)
			}
			continue
		}
		if current == nil {
			t.Fatalf("fixture line %d: offer field before [[offers]]", lineNo)
		}
		var str string
		var number float64
		switch key {
		case "retailer", "offer_name", "clean_name", "base_unit", "description", "department":
			str, err = strconv.Unquote(raw)
			if err == nil {
				switch key {
				case "retailer":
					current.Retailer = str
				case "offer_name":
					current.OfferName = str
				case "clean_name":
					current.CleanName = str
				case "base_unit":
					current.BaseUnit = str
				case "description":
					current.Description = str
				case "department":
					current.Department = str
				}
			}
		case "is_app_price":
			current.IsAppPrice, err = strconv.ParseBool(raw)
		case "price", "unit_price", "normal_price", "rel_savings":
			number, err = strconv.ParseFloat(raw, 64)
			if err == nil {
				v := number
				switch key {
				case "price":
					current.Price = &v
				case "unit_price":
					current.UnitPrice = &v
				case "normal_price":
					current.NormalPrice = &v
				case "rel_savings":
					current.RelSavings = &v
				}
			}
		default:
			// The captured file may include extra API metadata not used by these tests.
			continue
		}
		if err != nil {
			t.Fatalf("fixture line %d: %v", lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if len(fixture.Offers) == 0 {
		t.Fatal("fixture has no offers")
	}
	return fixture
}

// searchOffer adapts one real offer into the engine's representation —
// the same adapter shape the integration boundary uses. Retailer
// boilerplate tails ("Bemærk: føtex <butiksliste>") are stripped: store
// location lists are not product text and leak place names like
// "Carlsberg Byen" into searchable fields.
func (r realOffer) searchOffer() SearchOffer {
	offer := SearchOffer{
		ID:          r.OfferName,
		Name:        r.CleanName,
		Description: trimRetailerBoilerplate(r.Description),
		Store:       r.Retailer,
		Department:  r.Department,
		IsAppPrice:  r.IsAppPrice,
	}
	if r.Price != nil {
		ore := Money(math.Round(*r.Price * 100))
		offer.Price = &ore
	}
	if r.NormalPrice != nil {
		ore := Money(math.Round(*r.NormalPrice * 100))
		offer.NormalPrice = &ore
	}
	if r.UnitPrice != nil {
		family := UnitPiece
		switch r.BaseUnit {
		case "kilogram":
			family = UnitMass
		case "liter":
			family = UnitVolume
		}
		offer.UnitPrice = &UnitPrice{
			OrePerBasis: int64(math.Round(*r.UnitPrice * 100)),
			Family:      family,
		}
	}
	return offer
}

// trimRetailerBoilerplate cuts retailer boilerplate tails from offer
// descriptions. føtex descriptions end with a store-availability note
// ("Bemærk: føtex <butiksliste>") whose location names ("Carlsberg
// Byen") are not product text and leak into brand matching.
func trimRetailerBoilerplate(description string) string {
	for _, marker := range []string{"Bemærk:", "Bemærk :", "bemærk:"} {
		if idx := strings.Index(description, marker); idx > 0 {
			return strings.TrimSpace(description[:idx])
		}
	}
	return description
}

// runQuery compiles a query and returns the real offers that match.
func runQuery(t *testing.T, raw string, fixture realFixture) []realOffer {
	cfg := DefaultConfig()
	compiled := mustCompile(t, raw, cfg)
	var matched []realOffer
	for _, r := range fixture.Offers {
		if Match(compiled, Prepare(r.searchOffer(), cfg)).Matched {
			matched = append(matched, r)
		}
	}
	return matched
}

func TestRealOffersBeerWithoutJunk(t *testing.T) {
	fixture := loadRealOffers(t)
	matched := runQuery(t, "øl -alkoholfri -julekalender", fixture)

	// The fixture must actually contain beer for this test to bite.
	if len(matched) == 0 {
		t.Fatal("expected beer matches in the real fixture")
	}
	for _, r := range matched {
		name := Normalize(r.CleanName)
		if strings.Contains(name, "alkoholfri") {
			t.Fatalf("alcohol-free offer leaked past -alkoholfri: %q", r.CleanName)
		}
		if strings.Contains(name, "julekalender") {
			t.Fatalf("advent calendar leaked past -julekalender: %q", r.CleanName)
		}
	}
}

func TestRealOffersCocaColaZero(t *testing.T) {
	fixture := loadRealOffers(t)
	matched := runQuery(t, `"coca cola" -zero`, fixture)

	sawCola := false
	for _, r := range matched {
		name := Normalize(r.CleanName)
		if strings.Contains(name, "zero") {
			t.Fatalf("coca cola zero leaked past -zero: %q", r.CleanName)
		}
		if strings.Contains(name, "coca cola") {
			sawCola = true
		}
	}
	if !sawCola {
		t.Skip("no regular coca cola offer in this publication")
	}
}

func TestRealOffersConceptExpansion(t *testing.T) {
	fixture := loadRealOffers(t)

	// Querying the concept "øl" must find beer-worded offers AND offers
	// that only carry a configured brand alias (e.g. Carlsberg).
	viaConcept := runQuery(t, "øl", fixture)
	viaLiteral := runQuery(t, `"øl"`, fixture)

	literal := map[string]bool{}
	for _, r := range viaLiteral {
		literal[r.OfferName] = true
	}
	for _, r := range viaConcept {
		if !literal[r.OfferName] {
			return // expansion reached an alias-only offer: real value
		}
	}
	if len(viaConcept) != len(viaLiteral) {
		t.Fatalf("concept expansion (%d) is not a superset of literal matching (%d)",
			len(viaConcept), len(viaLiteral))
	}
	t.Log("note: expansion added no new real offers this week")
}

func TestRealOffersIncludeEverySupportedRetailer(t *testing.T) {
	fixture := loadRealOffers(t)
	counts := map[string]int{}
	for _, offer := range fixture.Offers {
		counts[offer.Retailer]++
	}
	// The fixture is refreshed with cmd/dealfetch, which covers all three
	// feeds. A missing Lidl section means the capture tool regressed.
	for _, retailer := range []string{"spar", "fotex", "lidl"} {
		if counts[retailer] == 0 {
			t.Fatalf("fixture has no %s offers (counts %v); run cmd/dealfetch", retailer, counts)
		}
	}
}

func TestRealOffersLidlStoreFilter(t *testing.T) {
	fixture := loadRealOffers(t)
	matched := runQuery(t, "store:lidl", fixture)
	if len(matched) == 0 {
		t.Fatal("store:lidl must match the captured Lidl publication")
	}
	for _, r := range matched {
		if r.Retailer != "lidl" {
			t.Fatalf("store:lidl matched %q from %q", r.CleanName, r.Retailer)
		}
	}
}

func TestRealOffersLidlConcepts(t *testing.T) {
	fixture := loadRealOffers(t)
	// Concepts must reach Lidl wording too: MADVÆRKET/ROSE use Danish
	// product words, while brand-only lines (TUBORG, Kit Kat) need aliases.
	for _, query := range []string{"kylling", "ost", "sodavand", "slik"} {
		matched := runQuery(t, query, fixture)
		sawLidl := false
		for _, r := range matched {
			if r.Retailer == "lidl" {
				sawLidl = true
				break
			}
		}
		if !sawLidl {
			t.Fatalf("concept %q matched no Lidl offers this week", query)
		}
	}
}

func TestRealOffersPriceFilter(t *testing.T) {
	fixture := loadRealOffers(t)
	matched := runQuery(t, "slik max:50", fixture)
	for _, r := range matched {
		if r.Price == nil {
			t.Fatalf("max:50 matched offer without price: %q", r.CleanName)
		}
		if *r.Price > 50.0 {
			t.Fatalf("max:50 matched %q at %v", r.CleanName, *r.Price)
		}
	}
}

func TestRealOffersUnitPriceFamily(t *testing.T) {
	fixture := loadRealOffers(t)
	matched := runQuery(t, "oksekød unitprice:kg:<150", fixture)
	for _, r := range matched {
		if r.BaseUnit != "kilogram" {
			t.Fatalf("unitprice:kg: matched %q with base unit %q", r.CleanName, r.BaseUnit)
		}
		if *r.UnitPrice >= 150 {
			t.Fatalf("unitprice:kg:<150 matched %q at %v/kg", r.CleanName, *r.UnitPrice)
		}
	}
}

func TestRealOffersStoreFilter(t *testing.T) {
	fixture := loadRealOffers(t)
	matched := runQuery(t, "ost store:føtex", fixture)
	if len(matched) == 0 {
		t.Skip("no føtex cheese offers this week")
	}
	for _, r := range matched {
		if r.Retailer != "fotex" {
			t.Fatalf("store:føtex matched %q from %q", r.CleanName, r.Retailer)
		}
	}
}

func TestRealOffersDiscountFilter(t *testing.T) {
	fixture := loadRealOffers(t)
	cfg := DefaultConfig()
	matched := runQuery(t, "discount:>=10%", fixture)
	for _, r := range matched {
		prepared := Prepare(r.searchOffer(), cfg)
		// Discount may come from explicit savings or be derived from the
		// normal price; either is a valid signal.
		if prepared.Discount == nil {
			t.Fatalf("discount matched offer without discount signal: %q", r.CleanName)
		}
		if *prepared.Discount < 100 {
			t.Fatalf("discount:>=10%% matched %q at %s off", r.CleanName, prepared.Discount.Pct())
		}
	}
	t.Logf("offers with >=10%% discount: %d", len(matched))
}

func TestRealOffersNoFalsePositiveOnShortWords(t *testing.T) {
	fixture := loadRealOffers(t)
	cfg := DefaultConfig()

	// Short words must never match inside compounds: "is" is not "gris".
	isCompiled := mustCompile(t, "is", cfg)
	for _, name := range []string{"Gris i fad", "Risengrød", "Friskpresset juice"} {
		if Match(isCompiled, Prepare(SearchOffer{Name: name}, cfg)).Matched {
			t.Fatalf("short-word false positive: %q matched 'is'", name)
		}
	}

	// Real matches must be legitimate (clean token or configured alias).
	for _, r := range runQuery(t, "is", fixture) {
		result := MatchDebug(isCompiled, Prepare(r.searchOffer(), cfg))
		if len(result.Reasons) == 0 {
			t.Fatalf("is matched %q with no recorded reason", r.CleanName)
		}
	}
}
