package search

import (
	"sort"
	"strconv"
	"strings"
)

// Filter is one typed structured filter from a query. Implementations
// carry parsed values only (never raw strings) and evaluate against a
// prepared offer. New filter types plug in through RegisterFilter without
// parser or matcher changes.
type Filter interface {
	// Key returns the canonical filter key ("price", "unitprice", ...).
	Key() string
	// Explain returns the canonical query spelling, e.g. "price:<50".
	Explain() string
	// Matches reports whether the offer satisfies the filter. missingFails
	// selects the documented behavior for missing structured data: when
	// true an offer without the required field fails the filter, when
	// false missing data never vetoes.
	Matches(offer PreparedOffer, missingFails bool) bool
}

// filterParser turns a filter value string into a typed Filter. cfg
// supplies alias tables so values like store:"føtex" canonicalize to the
// same identifier as the offer side.
type filterParser func(cfg Config, value string) (Filter, error)

var filterParsers = map[string]filterParser{}

// RegisterFilter adds or replaces a filter key. Values are parsed by fn,
// which must return a typed Filter or an *ParseError. Registration is
// intended for program setup; it is not synchronized for concurrent use.
func RegisterFilter(key string, fn filterParser) {
	filterParsers[strings.ToLower(strings.TrimSpace(key))] = fn
}

// KnownFilterKeys returns the registered filter keys sorted, for error
// messages and documentation.
func KnownFilterKeys() []string {
	keys := make([]string, 0, len(filterParsers))
	for key := range filterParsers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// lookupFilter returns the parser for a key, honoring key aliases like
// max: (sugar for price:<=).
func lookupFilter(key string) (filterParser, bool) {
	fn, ok := filterParsers[strings.ToLower(key)]
	return fn, ok
}

// ParseFilter parses one key:value filter expression through the
// registered parsers, for programmatic query construction outside the
// query-string parser. cfg supplies alias tables for store and
// department canonicalization.
func ParseFilter(key, value string, cfg Config) (Filter, error) {
	parser, ok := lookupFilter(key)
	if !ok {
		return nil, parseErrorf(ErrUnknownFilter, -1, key, "unknown filter key %q", key)
	}
	filter, err := parser(cfg, value)
	if err != nil {
		if parseErr, ok := err.(*ParseError); ok {
			return nil, parseErr
		}
		return nil, parseErrorf(ErrInvalidFilter, -1, value, "%v", err)
	}
	return filter, nil
}

// Numeric comparison helpers shared by the typed filters below.

// priceFilter compares the offer's actual price. It is the canonical
// representation of every price comparison, including the max:/min:
// parser sugar.
type priceFilter struct {
	op  Op
	ore Money
}

func (f priceFilter) Key() string { return "price" }

func (f priceFilter) Explain() string {
	return "price:" + f.op.String() + moneyQuery(f.ore)
}

func (f priceFilter) Matches(offer PreparedOffer, missingFails bool) bool {
	if offer.Offer.Price == nil {
		return !missingFails
	}
	return f.op.Holds(int64(*offer.Offer.Price), int64(f.ore))
}

// unitPriceFilter compares a normalized unit price. It never compares
// across unit families: when Family is set, the offer's unit price must
// have the same family to match at all.
type unitPriceFilter struct {
	op     Op
	ore    int64 // øre per basis unit
	family UnitFamily
}

func (f unitPriceFilter) Key() string { return "unitprice" }

func (f unitPriceFilter) Explain() string {
	key := "unitprice"
	if f.family != UnitAny {
		key += ":" + f.family.String()
	}
	return key + ":" + f.op.String() + moneyQuery(Money(f.ore))
}

func (f unitPriceFilter) Matches(offer PreparedOffer, missingFails bool) bool {
	up := offer.Offer.UnitPrice
	if up == nil {
		return !missingFails
	}
	if f.family != UnitAny && up.Family != f.family {
		// Incompatible bases are never compared.
		return false
	}
	return f.op.Holds(up.OrePerBasis, f.ore)
}

// discountFilter compares the offer's discount percentage (direct or
// derived from price and normal price at prepare time).
type discountFilter struct {
	op       Op
	permille Percentage
}

func (f discountFilter) Key() string { return "discount" }

func (f discountFilter) Explain() string {
	return "discount:" + f.op.String() + f.permille.Pct()
}

func (f discountFilter) Matches(offer PreparedOffer, missingFails bool) bool {
	if offer.Discount == nil {
		return !missingFails
	}
	return f.op.Holds(int64(*offer.Discount), int64(f.permille))
}

// storeFilter matches the offer's canonical store identifier.
type storeFilter struct {
	canonical string
	display   string
}

func (f storeFilter) Key() string { return "store" }

func (f storeFilter) Explain() string { return "store:" + f.display }

func (f storeFilter) Matches(offer PreparedOffer, _ bool) bool {
	return offer.StoreCanonical != "" && offer.StoreCanonical == f.canonical
}

// departmentFilter matches the offer's canonical department identifier.
type departmentFilter struct {
	canonical string
	display   string
}

func (f departmentFilter) Key() string { return "dept" }

func (f departmentFilter) Explain() string { return "dept:" + f.display }

func (f departmentFilter) Matches(offer PreparedOffer, _ bool) bool {
	return offer.DepartmentCanonical != "" && offer.DepartmentCanonical == f.canonical
}

// appFilter matches app-price offers (app:true) or non-app offers
// (app:false).
type appFilter struct {
	isApp bool
}

func (f appFilter) Key() string { return "app" }

func (f appFilter) Explain() string {
	if f.isApp {
		return "app:true"
	}
	return "app:false"
}

func (f appFilter) Matches(offer PreparedOffer, _ bool) bool {
	return offer.Offer.IsAppPrice == f.isApp
}

// moneyQuery renders øre in the decimal-comma query spelling.
func moneyQuery(ore Money) string {
	if ore%100 == 0 {
		return strconv.FormatInt(int64(ore)/100, 10)
	}
	sign := ""
	v := int64(ore)
	if v < 0 {
		sign = "-"
		v = -v
	}
	var builder strings.Builder
	builder.Grow(8)
	builder.WriteString(sign)
	builder.WriteString(strconv.FormatInt(v/100, 10))
	builder.WriteByte(',')
	frac := v % 100
	if frac < 10 {
		builder.WriteByte('0')
	}
	builder.WriteString(strconv.FormatInt(frac, 10))
	return builder.String()
}

// parseNumericValue splits an operator + number value like "<=49,95" into
// typed parts. An optional trailing "%" requests percent parsing.
func parseNumericValue(key, value string, percent bool) (Op, int64, error) {
	op, rest := splitOp(value)
	if rest == "" {
		return 0, 0, parseErrorf(ErrInvalidNumber, -1, value, "missing value for %s:", key)
	}
	if percent {
		p, err := ParsePercentage(rest)
		if err != nil {
			return 0, 0, parseErrorf(ErrInvalidNumber, -1, rest, "%v", err)
		}
		return op, int64(p), nil
	}
	m, err := ParseMoney(rest)
	if err != nil {
		return 0, 0, parseErrorf(ErrInvalidNumber, -1, rest, "%v", err)
	}
	return op, int64(m), nil
}

func init() {
	RegisterFilter("price", func(_ Config, value string) (Filter, error) {
		op, ore, err := parseNumericValue("price", value, false)
		if err != nil {
			return nil, err
		}
		return priceFilter{op: op, ore: Money(ore)}, nil
	})
	// max:50 and min:50 are parser sugar for price:<=50 and price:>=50.
	// The internal representation is always the canonical price filter.
	RegisterFilter("max", func(_ Config, value string) (Filter, error) {
		op, ore, err := parseNumericValue("price", value, false)
		if err != nil {
			return nil, err
		}
		if op != OpEQ {
			return nil, parseErrorf(ErrInvalidFilter, -1, value, "max: takes a bare amount")
		}
		return priceFilter{op: OpLE, ore: Money(ore)}, nil
	})
	RegisterFilter("min", func(_ Config, value string) (Filter, error) {
		op, ore, err := parseNumericValue("price", value, false)
		if err != nil {
			return nil, err
		}
		if op != OpEQ {
			return nil, parseErrorf(ErrInvalidFilter, -1, value, "min: takes a bare amount")
		}
		return priceFilter{op: OpGE, ore: Money(ore)}, nil
	})
	RegisterFilter("unitprice", func(_ Config, value string) (Filter, error) {
		family := UnitAny
		// Optional family qualifier: unitprice:kg:<80.
		if before, after, found := strings.Cut(value, ":"); found {
			parsed, ok := ParseUnitFamily(before)
			if !ok {
				return nil, parseErrorf(ErrInvalidFilter, -1, before,
					"unknown unit family %q (use kg, l or pc)", before)
			}
			family = parsed
			value = strings.TrimSpace(after)
		}
		op, ore, err := parseNumericValue("unitprice", value, false)
		if err != nil {
			return nil, err
		}
		return unitPriceFilter{op: op, ore: ore, family: family}, nil
	})
	RegisterFilter("discount", func(_ Config, value string) (Filter, error) {
		op, permille, err := parseNumericValue("discount", value, true)
		if err != nil {
			return nil, err
		}
		return discountFilter{op: op, permille: Percentage(permille)}, nil
	})
	storeParser := func(cfg Config, value string) (Filter, error) {
		display := strings.TrimSpace(value)
		if display == "" {
			return nil, parseErrorf(ErrInvalidFilter, -1, value, "store: needs a value")
		}
		return storeFilter{canonical: canonicalize(display, cfg.StoreAliases), display: display}, nil
	}
	RegisterFilter("store", storeParser)
	RegisterFilter("butik", storeParser) // Danish alias, canonical key is store
	deptParser := func(cfg Config, value string) (Filter, error) {
		display := strings.TrimSpace(value)
		if display == "" {
			return nil, parseErrorf(ErrInvalidFilter, -1, value, "dept: needs a value")
		}
		return departmentFilter{canonical: canonicalize(display, cfg.DepartmentAliases), display: display}, nil
	}
	RegisterFilter("dept", deptParser)
	RegisterFilter("department", deptParser)
	RegisterFilter("afdeling", deptParser)
	RegisterFilter("app", func(_ Config, value string) (Filter, error) {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "ja":
			return appFilter{isApp: true}, nil
		case "0", "false", "no", "nej":
			return appFilter{isApp: false}, nil
		}
		return nil, parseErrorf(ErrInvalidFilter, -1, value,
			"app: accepts true/false (or ja/nej)")
	})
}
