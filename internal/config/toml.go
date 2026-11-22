package config

import (
	"strconv"
	"strings"
)

func stripComment(line string) string {
	quoted := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = quoted
		case '"':
			quoted = !quoted
		case '#':
			if !quoted {
				return line[:i]
			}
		}
	}
	return line
}

func parseInt64(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func parseTomlString(value string) string {
	value = strings.TrimSpace(value)
	if !isQuotedValue(value) {
		return value
	}

	var builder strings.Builder
	escaped := false
	for _, r := range value[1 : len(value)-1] {
		if escaped {
			switch r {
			case 'n':
				builder.WriteRune('\n')
			case 'r':
				builder.WriteRune('\r')
			case 't':
				builder.WriteRune('\t')
			case '"':
				builder.WriteRune('"')
			case '\\':
				builder.WriteRune('\\')
			default:
				builder.WriteRune(r)
			}
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
		} else {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// isQuotedValue reports whether value is a complete "..." string. A trailing
// quote that is itself escaped ("a\\\"") does not terminate the string, so such
// input is returned raw instead of being silently unescaped.
func isQuotedValue(value string) bool {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	backslashes := 0
	for index := len(value) - 2; index >= 0 && value[index] == '\\'; index-- {
		backslashes++
	}
	return backslashes%2 == 0
}

func parseTomlStringArray(value string) []string {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != '[' || value[len(value)-1] != ']' {
		return nil
	}

	items := make([]string, 0)
	quoted := false
	escaped := false
	var builder strings.Builder
	for _, r := range value[1 : len(value)-1] {
		if !quoted {
			if r == '"' {
				quoted = true
				builder.Reset()
			}
			continue
		}

		if escaped {
			switch r {
			case 'n':
				builder.WriteRune('\n')
			case 'r':
				builder.WriteRune('\r')
			case 't':
				builder.WriteRune('\t')
			case '"':
				builder.WriteRune('"')
			case '\\':
				builder.WriteRune('\\')
			default:
				builder.WriteRune(r)
			}
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '"':
			quoted = false
			items = append(items, builder.String())
		default:
			builder.WriteRune(r)
		}
	}
	return items
}

// parseTomlFloat parses a bare TOML float or integer.
func parseTomlFloat(value string) (float64, bool) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// parseTomlBool parses a bare TOML boolean.
func parseTomlBool(value string) (bool, bool) {
	switch strings.TrimSpace(value) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func formatTomlFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func escapeTomlString(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\r", "\\r")
	value = strings.ReplaceAll(value, "\t", "\\t")
	return value
}

func formatTomlStringArray(values []string) string {
	if len(values) == 0 {
		return "[]"
	}

	var builder strings.Builder
	builder.WriteString("[\n")
	for _, value := range values {
		builder.WriteString(`  "`)
		builder.WriteString(escapeTomlString(value))
		builder.WriteString("\",\n")
	}
	builder.WriteString("]")
	return builder.String()
}
