package search

import (
	"fmt"
	"strings"
)

// PositiveMode selects how multiple positive terms combine.
type PositiveMode int

// Supported positive-term semantics.
const (
	// ModeAll requires every positive term to match (AND). This is the
	// documented default for the query language: "hakket oksekød" as two
	// terms requires both words, and users quote phrases explicitly.
	ModeAll PositiveMode = iota
	// ModeAny accepts an offer when at least one positive term matches
	// (OR). Legacy keyword wants are converted with this mode so old
	// multi-keyword wants keep behaving like alternatives.
	ModeAny
)

// String returns the mode name used in debug output.
func (m PositiveMode) String() string {
	if m == ModeAny {
		return "any"
	}
	return "all"
}

// Term is one parsed text term: positive or excluded, single word or
// quoted phrase. Value keeps the raw user text; normalization and
// concept expansion happen at compile time.
type Term struct {
	Value    string
	Excluded bool
	Phrase   bool
}

// Query is the stable internal query model produced by the parser (or
// built programmatically by compatibility adapters). Filters use typed
// representations, never raw strings.
type Query struct {
	Terms   []Term
	Filters []Filter
	Raw     string
	// Mode combines positive terms; zero value is ModeAll.
	Mode PositiveMode
}

// ErrCode is a stable machine-readable parse/compile error code.
type ErrCode string

// Error codes returned in ParseError.Code.
const (
	ErrEmptyQuery        ErrCode = "empty_query"
	ErrUnclosedQuote     ErrCode = "unclosed_quote"
	ErrEmptyTerm         ErrCode = "empty_term"
	ErrUnknownFilter     ErrCode = "unknown_filter"
	ErrInvalidNumber     ErrCode = "invalid_number"
	ErrInvalidFilter     ErrCode = "invalid_filter"
	ErrExcludedFilter    ErrCode = "excluded_filter"
	ErrQueryTooLong      ErrCode = "query_too_long"
	ErrTooManyTerms      ErrCode = "too_many_terms"
	ErrTooManyFilters    ErrCode = "too_many_filters"
	ErrPhraseTooLong     ErrCode = "phrase_too_long"
	ErrExpansionTooLarge ErrCode = "expansion_too_large"
)

// ParseError is an inspectable parse or validation error. Callers can use
// Code for programmatic handling and UserMessage for a Discord reply
// without parsing Error()'s text.
type ParseError struct {
	Code   ErrCode
	Token  string // the offending token or value
	Pos    int    // byte offset of the token in the raw query, -1 if unknown
	Detail string // developer-facing explanation
}

// Error implements error.
func (e *ParseError) Error() string {
	where := ""
	if e.Pos >= 0 {
		where = fmt.Sprintf(" at offset %d", e.Pos)
	}
	if e.Token != "" {
		return fmt.Sprintf("query parse error %s%s (token %q): %s", e.Code, where, e.Token, e.Detail)
	}
	return fmt.Sprintf("query parse error %s%s: %s", e.Code, where, e.Detail)
}

// UserMessage returns a short Danish message suitable for end users.
func (e *ParseError) UserMessage() string {
	switch e.Code {
	case ErrEmptyQuery:
		return "Søgningen er tom. Skriv mindst ét søgeord."
	case ErrUnclosedQuote:
		return "Der mangler et afsluttende \" i søgningen."
	case ErrEmptyTerm:
		return "Et søgeord er tomt."
	case ErrUnknownFilter:
		return fmt.Sprintf("Ukendt filter %q. Kendte filtre: %s.", e.Token, strings.Join(KnownFilterKeys(), ", "))
	case ErrInvalidNumber:
		return fmt.Sprintf("Ugyldigt tal %q i filteret.", e.Token)
	case ErrInvalidFilter:
		return fmt.Sprintf("Ugyldig filterværdi %q.", e.Token)
	case ErrExcludedFilter:
		return "Filtre kan ikke være negativet (sæt ikke - foran pris:/store: osv.)."
	case ErrQueryTooLong:
		return "Søgningen er for lang."
	case ErrTooManyTerms:
		return "Søgningen har for mange ord."
	case ErrTooManyFilters:
		return "Søgningen har for mange filtre."
	case ErrPhraseTooLong:
		return "En af fraserne er for lang."
	case ErrExpansionTooLarge:
		return "Søgningen udvider til for mange synonymer."
	}
	return "Uforståelig søgning."
}

// parseErrorf builds a ParseError with a developer-facing detail string.
func parseErrorf(code ErrCode, pos int, token, format string, args ...any) *ParseError {
	return &ParseError{Code: code, Token: token, Pos: pos, Detail: fmt.Sprintf(format, args...)}
}
