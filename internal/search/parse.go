package search

import (
	"strings"
	"unicode/utf8"
)

// ParseQuery parses a query string into the stable internal Query model
// using DefaultConfig limits and filter registry. See ParseQueryWithConfig
// for the grammar.
func ParseQuery(input string) (Query, error) {
	return ParseQueryWithConfig(input, DefaultConfig())
}

// ParseQueryWithConfig parses the query language:
//
//	øl -alkoholfri -julekalender      positive term + exclusions
//	"coca cola" -zero                 quoted phrase + exclusion
//	"hakket oksekød" -økologisk       phrase + excluded concept
//	kaffe max:50                      term + sugar filter (price:<=50)
//	oksekød unitprice:kg:<80          term + typed unit-price filter
//	øl price:<100 discount:>=20%      term + typed numeric filters
//	cola store:føtex -zero            term + store filter + exclusion
//
// Grammar: whitespace separates unquoted tokens; double quotes group a
// phrase; a leading '-' excludes a token or quoted phrase; known
// key:value expressions become typed filters. Unknown key:value
// expressions are a parse error (never silently reinterpreted), so typos
// surface before matching. Filters cannot be excluded: '-price:...' is an
// explicit error, not a negative filter.
//
// Malformed quotes, invalid numbers, unsupported filter values and limit
// violations return *ParseError with stable codes and user-facing Danish
// messages; the returned Query is never partially valid.
func ParseQueryWithConfig(input string, cfg Config) (Query, error) {
	raw := input
	if utf8.RuneCountInString(input) > cfg.Limits.MaxQueryRunes {
		return Query{}, parseErrorf(ErrQueryTooLong, -1, "",
			"query is %d runes, limit is %d", utf8.RuneCountInString(input), cfg.Limits.MaxQueryRunes)
	}

	query := Query{Raw: raw, Mode: cfg.Mode}
	termCount := 0

	tokens, err := tokenize(input, cfg)
	if err != nil {
		return Query{}, err
	}

	for _, tok := range tokens {
		if strings.Contains(tok.text, ":") && !tok.quoted && isFilterCandidate(tok.text) {
			filter, err := parseFilterToken(tok)
			if err != nil {
				return Query{}, err
			}
			if len(query.Filters) >= cfg.Limits.MaxFilters {
				return Query{}, parseErrorf(ErrTooManyFilters, tok.pos, tok.text,
					"more than %d filters", cfg.Limits.MaxFilters)
			}
			query.Filters = append(query.Filters, filter)
			continue
		}

		value := strings.TrimSpace(tok.text)
		if value == "" {
			return Query{}, parseErrorf(ErrEmptyTerm, tok.pos, tok.text, "empty term")
		}
		if Normalize(value) == "" {
			return Query{}, parseErrorf(ErrEmptyTerm, tok.pos, tok.text,
				"term has no letters or digits")
		}
		if tok.quoted && utf8.RuneCountInString(value) > cfg.Limits.MaxPhraseRunes {
			return Query{}, parseErrorf(ErrPhraseTooLong, tok.pos, value,
				"phrase is %d runes, limit is %d", utf8.RuneCountInString(value), cfg.Limits.MaxPhraseRunes)
		}
		if termCount >= cfg.Limits.MaxTerms {
			return Query{}, parseErrorf(ErrTooManyTerms, tok.pos, value,
				"more than %d terms", cfg.Limits.MaxTerms)
		}
		termCount++
		query.Terms = append(query.Terms, Term{
			Value:    value,
			Excluded: tok.excluded,
			Phrase:   tok.quoted,
		})
	}

	if termCount == 0 && len(query.Filters) == 0 {
		return Query{}, parseErrorf(ErrEmptyQuery, -1, "", "query has no terms or filters")
	}
	return query, nil
}

// token is one raw query token with its source position and shape.
type token struct {
	text     string
	quoted   bool
	excluded bool
	pos      int
	cfg      Config // alias tables for filter canonicalization
}

// tokenize splits the input into tokens honoring quotes and exclusion
// prefixes. Unclosed quotes are a parse error.
func tokenize(input string, cfg Config) ([]token, error) {
	var tokens []token
	runes := []rune(input)
	i := 0
	for i < len(runes) {
		for i < len(runes) && isSpace(runes[i]) {
			i++
		}
		if i >= len(runes) {
			break
		}
		start := i
		excluded := false
		if runes[i] == '-' && i+1 < len(runes) && !isSpace(runes[i+1]) {
			excluded = true
			i++
		}
		quoted := false
		var text strings.Builder
		if i < len(runes) && runes[i] == '"' {
			quoted = true
			i++
			closed := false
			for i < len(runes) {
				if runes[i] == '"' {
					closed = true
					i++
					break
				}
				text.WriteRune(runes[i])
				i++
			}
			if !closed {
				return nil, parseErrorf(ErrUnclosedQuote, start, string(runes[start:]),
					"unterminated quote")
			}
		} else {
			for i < len(runes) && !isSpace(runes[i]) {
				// A quote inside an unquoted token ends the word and is
				// rejected: mixing forms like foo"bar" is malformed.
				if runes[i] == '"' {
					return nil, parseErrorf(ErrUnclosedQuote, start, string(runes[start:]),
						"quote must start the token")
				}
				text.WriteRune(runes[i])
				i++
			}
		}
		if excluded && !quoted && text.String() == "" {
			// A bare "-" is not a term and not an exclusion.
			return nil, parseErrorf(ErrEmptyTerm, start, "-", "bare exclusion without a term")
		}
		tokens = append(tokens, token{
			text:     text.String(),
			quoted:   quoted,
			excluded: excluded,
			pos:      start,
			cfg:      cfg,
		})
	}
	return tokens, nil
}

// isFilterCandidate reports whether an unquoted token should be parsed as
// key:value. Only letter keys qualify, so times ("10:30") and URLs stay
// ordinary terms.
func isFilterCandidate(text string) bool {
	key, _, found := strings.Cut(text, ":")
	if !found || key == "" {
		return false
	}
	for _, r := range key {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// parseFilterToken parses one key:value token into a typed Filter. The
// exclusion prefix on a filter is an explicit error.
func parseFilterToken(tok token) (Filter, error) {
	if tok.excluded {
		return nil, parseErrorf(ErrExcludedFilter, tok.pos, tok.text,
			"filters cannot be excluded")
	}
	key, value, _ := strings.Cut(tok.text, ":")
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	parser, ok := lookupFilter(key)
	if !ok {
		return nil, parseErrorf(ErrUnknownFilter, tok.pos, key,
			"unknown filter key %q", key)
	}
	filter, err := parser(tok.cfg, value)
	if err != nil {
		if parseErr, ok := err.(*ParseError); ok {
			if parseErr.Pos < 0 {
				parseErr.Pos = tok.pos
			}
			if parseErr.Token == "" {
				parseErr.Token = value
			}
			return nil, parseErr
		}
		return nil, parseErrorf(ErrInvalidFilter, tok.pos, value, "%v", err)
	}
	return filter, nil
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// HasQuerySyntax reports whether the text uses query-language syntax:
// quotes, exclusion prefixes or key:value filters. Text without these
// markers is safe to keep treating as a legacy keyword phrase.
func HasQuerySyntax(text string) bool {
	if strings.Contains(text, `"`) {
		return true
	}
	tokens, err := tokenize(text, DefaultConfig())
	if err != nil {
		// Malformed syntax is still syntax; let parsing report the error.
		return true
	}
	for _, tok := range tokens {
		if tok.excluded || tok.quoted {
			return true
		}
		if isFilterCandidate(tok.text) {
			return true
		}
	}
	return false
}
