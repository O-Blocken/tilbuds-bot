package search

import (
	"errors"
	"fmt"
	"strings"
)

// Leaf parse errors, defined statically so callers can test them with
// errors.Is. Context (line numbers, offending text) is added at the
// call site by wrapping.
var (
	errTOMLUnterminatedArray   = errors.New("unterminated TOML array")
	errTOMLExpectedString      = errors.New("expected a quoted TOML string")
	errTOMLUnterminatedLiteral = errors.New("unterminated TOML literal string")
	errTOMLUnterminatedString  = errors.New("unterminated TOML string")
	errTOMLArrayComma          = errors.New("expected ',' between array items")
	errTOMLTrailingText        = errors.New("unexpected text after value")
	errTOMLExpectedKeyValue    = errors.New("expected key = value")
	errTOMLKeyOutsideTable     = errors.New("key outside any [[concepts]] table")
	errTOMLSingleString        = errors.New("value must be a single string")
	errTOMLUnknownKey          = errors.New("unknown key (expected name, description, aliases or refs)")
	errTOMLUnsupportedTable    = errors.New("unsupported table (only [[concepts]] is supported)")
	errTOMLValueOutsideTable   = errors.New("value outside [[concepts]]")
)

// ParseConcepts decodes a TOML concept registry.
//
// Supported TOML subset (deliberately strict, mirroring the project's
// config parser approach):
//
//	[[concepts]]          array-of-tables entries
//	name = "øl"           basic strings with \" \\ \n \t escapes
//	description = '...'   literal strings
//	aliases = ["a", "b"]  string arrays, may span lines
//	refs = [...]          string arrays
//
// Comments start with '#' outside strings. Anything else (other tables,
// inline tables, dotted keys, numbers, dates) is an explicit error rather
// than a silent re-reading.
func ParseConcepts(data []byte) ([]Concept, error) {
	lines := strings.Split(string(data), "\n")

	var concepts []Concept
	var current *Concept

	// Multi-line array accumulation state.
	var arrayKey string
	var arrayText strings.Builder

	for idx := range lines {
		lineNo := idx + 1
		line := strings.TrimSpace(stripTOMLComment(lines[idx]))

		if arrayKey != "" {
			arrayText.WriteByte(' ')
			arrayText.WriteString(line)
			text := arrayText.String()
			if !arrayClosed(text) {
				continue
			}
			values, err := parseTOMLArray(text)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			if err := setConceptField(current, arrayKey, values, lineNo); err != nil {
				return nil, err
			}
			arrayKey = ""
			arrayText.Reset()
			continue
		}

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[[") {
			if !strings.HasSuffix(line, "]]") || strings.TrimSpace(line[2:len(line)-2]) != "concepts" {
				return nil, fmt.Errorf("line %d: %w: %q", lineNo, errTOMLUnsupportedTable, line)
			}
			concepts = append(concepts, Concept{})
			current = &concepts[len(concepts)-1]
			continue
		}

		key, rawValue, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("line %d: %w: %q", lineNo, errTOMLExpectedKeyValue, line)
		}
		if current == nil {
			return nil, fmt.Errorf("line %d: %w: %q", lineNo, errTOMLKeyOutsideTable, strings.TrimSpace(key))
		}
		key = strings.TrimSpace(key)
		rawValue = strings.TrimSpace(rawValue)

		if strings.HasPrefix(rawValue, "[") {
			if arrayClosed(rawValue) {
				values, err := parseTOMLArray(rawValue)
				if err != nil {
					return nil, fmt.Errorf("line %d: %w", lineNo, err)
				}
				if err := setConceptField(current, key, values, lineNo); err != nil {
					return nil, err
				}
			} else {
				arrayKey = key
				arrayText.Reset()
				arrayText.WriteString(rawValue)
			}
			continue
		}

		value, err := parseTOMLString(rawValue)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if err := setConceptField(current, key, []string{value}, lineNo); err != nil {
			return nil, err
		}
	}

	if arrayKey != "" {
		return nil, fmt.Errorf("%w: %q", errTOMLUnterminatedArray, arrayKey)
	}
	return concepts, nil
}

// setConceptField assigns one parsed key on a concept. Unrecognized keys
// are errors so registry typos surface at load time.
func setConceptField(concept *Concept, key string, values []string, lineNo int) error {
	if concept == nil {
		return fmt.Errorf("line %d: %w", lineNo, errTOMLValueOutsideTable)
	}
	switch key {
	case "name":
		if len(values) != 1 {
			return fmt.Errorf("line %d: name: %w", lineNo, errTOMLSingleString)
		}
		concept.Name = values[0]
	case "description":
		if len(values) != 1 {
			return fmt.Errorf("line %d: description: %w", lineNo, errTOMLSingleString)
		}
		concept.Description = values[0]
	case "aliases":
		concept.Aliases = append(concept.Aliases, values...)
	case "exact_aliases":
		concept.ExactAliases = append(concept.ExactAliases, values...)
	case "refs":
		concept.Refs = append(concept.Refs, values...)
	default:
		return fmt.Errorf("line %d: %w: %q", lineNo, errTOMLUnknownKey, key)
	}
	return nil
}

// stripTOMLComment removes a trailing '#' comment outside quoted strings.
func stripTOMLComment(line string) string {
	inBasic := false
	inLiteral := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		switch {
		case inBasic:
			switch r {
			case '\\':
				escaped = true
			case '"':
				inBasic = false
			}
		case inLiteral:
			if r == '\'' {
				inLiteral = false
			}
		case r == '"':
			inBasic = true
		case r == '\'':
			inLiteral = true
		case r == '#':
			return line[:i]
		}
	}
	return line
}

// arrayClosed reports whether s contains a ']' that closes the array
// starting at s[0] == '[', ignoring brackets inside strings.
func arrayClosed(s string) bool {
	return findArrayEnd(s) >= 0
}

// findArrayEnd returns the index of the ']' closing the array that starts
// at s[0], or -1 while the array is still open. Strings are skipped.
func findArrayEnd(s string) int {
	if len(s) == 0 || s[0] != '[' {
		return -1
	}
	inBasic := false
	inLiteral := false
	escaped := false
	for i := 1; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch {
		case inBasic:
			switch s[i] {
			case '\\':
				escaped = true
			case '"':
				inBasic = false
			}
		case inLiteral:
			if s[i] == '\'' {
				inLiteral = false
			}
		case s[i] == '"':
			inBasic = true
		case s[i] == '\'':
			inLiteral = true
		case s[i] == ']':
			return i
		}
	}
	return -1
}

// parseTOMLArray parses "[\"a\", \"b\"]" into its string elements.
// Elements must all be strings; trailing commas are allowed.
func parseTOMLArray(s string) ([]string, error) {
	end := findArrayEnd(s)
	if end < 0 {
		return nil, errTOMLUnterminatedArray
	}
	inner := s[1:end]
	if strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s[end+1:]), "")) != "" {
		return nil, fmt.Errorf("%w: %q", errTOMLTrailingText, strings.TrimSpace(s[end+1:]))
	}

	var values []string
	for {
		inner = strings.TrimSpace(inner)
		if inner == "" {
			return values, nil
		}
		value, rest, err := takeTOMLString(inner)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return values, nil
		}
		if rest[0] != ',' {
			return nil, fmt.Errorf("%w: %q", errTOMLArrayComma, rest)
		}
		inner = rest[1:]
	}
}

// parseTOMLString parses one quoted string ("..." or '...') and requires
// that nothing follows it.
func parseTOMLString(s string) (string, error) {
	value, rest, err := takeTOMLString(s)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(rest) != "" {
		return "", fmt.Errorf("%w: %q", errTOMLTrailingText, strings.TrimSpace(rest))
	}
	return value, nil
}

// takeTOMLString parses one quoted string from the front of s and returns
// the value with the unconsumed remainder.
func takeTOMLString(s string) (string, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", errTOMLExpectedString
	}
	if s[0] == '\'' {
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", "", errTOMLUnterminatedLiteral
		}
		return s[1 : 1+end], s[end+2:], nil
	}
	if s[0] != '"' {
		return "", "", fmt.Errorf("%w: %q", errTOMLExpectedString, s)
	}

	var builder strings.Builder
	escaped := false
	for i := 1; i < len(s); i++ {
		c := s[i]
		if escaped {
			switch c {
			case 'n':
				builder.WriteByte('\n')
			case 'r':
				builder.WriteByte('\r')
			case 't':
				builder.WriteByte('\t')
			case '"':
				builder.WriteByte('"')
			case '\\':
				builder.WriteByte('\\')
			default:
				builder.WriteByte(c)
			}
			escaped = false
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '"':
			return builder.String(), s[i+1:], nil
		default:
			builder.WriteByte(c)
		}
	}
	return "", "", errTOMLUnterminatedString
}
