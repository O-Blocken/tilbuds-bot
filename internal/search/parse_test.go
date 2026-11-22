package search

import (
	"errors"
	"testing"
)

func TestParseQueryTerms(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		terms   []Term
		filters int
	}{
		{
			name:  "single word",
			in:    "øl",
			terms: []Term{{Value: "øl"}},
		},
		{
			name:  "multiple words are AND terms",
			in:    "hakket oksekød",
			terms: []Term{{Value: "hakket"}, {Value: "oksekød"}},
		},
		{
			name:  "exclusion",
			in:    "øl -alkoholfri -julekalender",
			terms: []Term{{Value: "øl"}, {Value: "alkoholfri", Excluded: true}, {Value: "julekalender", Excluded: true}},
		},
		{
			name:  "quoted phrase",
			in:    `"coca cola" -zero`,
			terms: []Term{{Value: "coca cola", Phrase: true}, {Value: "zero", Excluded: true}},
		},
		{
			name:  "excluded phrase",
			in:    `"hakket oksekød" -"gammel ost"`,
			terms: []Term{{Value: "hakket oksekød", Phrase: true}, {Value: "gammel ost", Excluded: true, Phrase: true}},
		},
		{
			name:  "extra whitespace collapses",
			in:    "  øl   -zero  ",
			terms: []Term{{Value: "øl"}, {Value: "zero", Excluded: true}},
		},
		{
			name:  "unicode danish",
			in:    "franskbrød smør",
			terms: []Term{{Value: "franskbrød"}, {Value: "smør"}},
		},
		{
			name:    "term plus filters",
			in:      "øl price:<100 discount:>=20%",
			terms:   []Term{{Value: "øl"}},
			filters: 2,
		},
		{
			name:    "filters only",
			in:      "price:<50",
			terms:   nil,
			filters: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := ParseQuery(tt.in)
			if err != nil {
				t.Fatalf("ParseQuery(%q) error: %v", tt.in, err)
			}
			if len(q.Terms) != len(tt.terms) {
				t.Fatalf("terms = %+v, want %+v", q.Terms, tt.terms)
			}
			for i, want := range tt.terms {
				got := q.Terms[i]
				if got.Value != want.Value || got.Excluded != want.Excluded || got.Phrase != want.Phrase {
					t.Fatalf("term[%d] = %+v, want %+v", i, got, want)
				}
			}
			if len(q.Filters) != tt.filters {
				t.Fatalf("filters = %d, want %d", len(q.Filters), tt.filters)
			}
		})
	}
}

func TestParseQueryFilters(t *testing.T) {
	tests := []struct {
		in      string
		explain string
	}{
		{"price:<50", "price:<50"},
		{"price:<=100", "price:<=100"},
		{"price:>=20", "price:>=20"},
		{"price:>20", "price:>20"},
		{"price:=49,95", "price:=49,95"},
		// Sugar collapses to the canonical filter form.
		{"max:50", "price:<=50"},
		{"min:25", "price:>=25"},
		{"unitprice:<80", "unitprice:<80"},
		{"unitprice:kg:<80", "unitprice:kg:<80"},
		{"unitprice:l:<=12", "unitprice:l:<=12"},
		{"discount:>=25%", "discount:>=25%"},
		{"discount:>20%", "discount:>20%"},
		{"app:true", "app:true"},
		{"app:nej", "app:false"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			q, err := ParseQuery(tt.in)
			if err != nil {
				t.Fatalf("ParseQuery(%q) error: %v", tt.in, err)
			}
			if len(q.Filters) != 1 {
				t.Fatalf("want exactly 1 filter, got %d", len(q.Filters))
			}
			if got := q.Filters[0].Explain(); got != tt.explain {
				t.Fatalf("Explain() = %q, want %q", got, tt.explain)
			}
		})
	}
}

func TestParseQueryErrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		code ErrCode
	}{
		{"empty", "", ErrEmptyQuery},
		{"whitespace only", "   ", ErrEmptyQuery},
		{"unclosed quote", `øl "coca cola`, ErrUnclosedQuote},
		{"quote mid token", `øl co"ca`, ErrUnclosedQuote},
		{"bare dash", "øl -", ErrEmptyTerm},
		{"empty phrase", `""`, ErrEmptyTerm},
		{"unknown filter", "øl colour:red", ErrUnknownFilter},
		{"invalid number", "price:abc", ErrInvalidNumber},
		{"missing number", "price:<", ErrInvalidNumber},
		{"too many decimals", "price:49,999", ErrInvalidNumber},
		{"invalid percent", "discount:abc%", ErrInvalidNumber},
		{"percent over 100", "discount:>150%", ErrInvalidNumber},
		{"excluded filter", "-price:<50", ErrExcludedFilter},
		{"invalid app value", "app:maybe", ErrInvalidFilter},
		{"unknown unit family", "unitprice:quart:<5", ErrInvalidFilter},
		{"max with operator", "max:<=50", ErrInvalidFilter},
		{"empty store", "store:", ErrInvalidFilter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseQuery(tt.in)
			if err == nil {
				t.Fatalf("ParseQuery(%q) expected error", tt.in)
			}
			var parseErr *ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("want *ParseError, got %T: %v", err, err)
			}
			if parseErr.Code != tt.code {
				t.Fatalf("code = %s, want %s (%v)", parseErr.Code, tt.code, err)
			}
			if parseErr.UserMessage() == "" {
				t.Fatal("UserMessage must be non-empty")
			}
		})
	}
}

// A malformed query must never produce a partially valid result
// .
func TestParseQueryErrorIsNeverPartial(t *testing.T) {
	q, err := ParseQuery(`øl price:<50 colour:red`)
	if err == nil {
		t.Fatal("query with an unknown filter must fail")
	}
	if len(q.Terms) != 0 || len(q.Filters) != 0 {
		t.Fatalf("failed parse must return a zero query, got %+v", q)
	}
}

func TestParseQueryLimits(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.MaxTerms = 2
	if _, err := ParseQueryWithConfig("a b c", cfg); err == nil {
		t.Fatal("3 terms must exceed the 2-term limit")
	}

	cfg = DefaultConfig()
	cfg.Limits.MaxFilters = 1
	if _, err := ParseQueryWithConfig("price:<5 max:10", cfg); err == nil {
		t.Fatal("2 filters must exceed the 1-filter limit")
	}

	cfg = DefaultConfig()
	cfg.Limits.MaxQueryRunes = 5
	if _, err := ParseQueryWithConfig("oksekød", cfg); err == nil {
		t.Fatal("long query must exceed the rune limit")
	}

	cfg = DefaultConfig()
	cfg.Limits.MaxPhraseRunes = 4
	if _, err := ParseQueryWithConfig(`"coca cola"`, cfg); err == nil {
		t.Fatal("long phrase must exceed the phrase limit")
	}
}

func TestParseQueryUnknownKeyIsError(t *testing.T) {
	// Documented behavior: unknown key:value expressions are explicit
	// parse errors, never silently reinterpreted as terms.
	_, err := ParseQuery("colour:red")
	if err == nil {
		t.Fatal("unknown key:value must error")
	}
	var parseErr *ParseError
	if errors.As(err, &parseErr) && parseErr.Code != ErrUnknownFilter {
		t.Fatalf("code = %s, want %s", parseErr.Code, ErrUnknownFilter)
	}
}

func TestParseQueryMode(t *testing.T) {
	q, err := ParseQuery("a b")
	if err != nil {
		t.Fatal(err)
	}
	if q.Mode != ModeAll {
		t.Fatalf("default mode = %v, want ModeAll", q.Mode)
	}
}
