package search

import (
	"fmt"
	"sort"
	"strings"
)

// CompiledTerm is one term resolved and expanded once, ready for
// evaluation against many offers. Aliases are normalized and sorted.
type CompiledTerm struct {
	// Original is the raw user text.
	Original string
	// Excluded marks hard exclusions.
	Excluded bool
	// Phrase marks quoted terms, which require phrase-level matching.
	Phrase bool
	// Aliases are the normalized alternatives to match: the normalized
	// term plus configured concept aliases. For literal terms this is
	// just the term itself.
	Aliases []string
	// ExactAliases is the subset of Aliases that must match whole words
	// only and never inside compound tokens.
	ExactAliases map[string]bool
	// Concepts lists the concept names this term expanded through.
	Concepts []string
}

// CompiledQuery is a validated, normalized query ready for evaluation.
// Compilation happens once per stored query, never per
// offer.
type CompiledQuery struct {
	Positive []CompiledTerm
	Negative []CompiledTerm
	Filters  []Filter
	// Raw is the original query string ("" for programmatic queries).
	Raw string
	// Mode combines positive terms (ALL by default).
	Mode PositiveMode
	// MissingFieldFails controls filter behavior for missing structured
	// data: true rejects such offers (default), false
	// lets them pass.
	MissingFieldFails bool
}

// CompileQuery validates and expands a parsed query. concept may be nil
// for literal-only matching. All validation errors surface here, before
// any offer is evaluated.
//
// With a nil Config the engine defaults are used.
func CompileQuery(q Query, concepts ConceptResolver, cfg Config) (CompiledQuery, error) {
	compiled := CompiledQuery{
		Raw:               q.Raw,
		Mode:              q.Mode,
		MissingFieldFails: cfg.MissingFieldFails,
		Filters:           append([]Filter(nil), q.Filters...),
	}

	var resolver *Registry
	if concepts != nil {
		if registry, ok := concepts.(*Registry); ok {
			resolver = registry
		}
	}

	for _, term := range q.Terms {
		compiledTerm := CompiledTerm{
			Original: term.Value,
			Excluded: term.Excluded,
			Phrase:   term.Phrase,
		}

		normalized := Normalize(term.Value)
		if normalized == "" {
			return CompiledQuery{}, parseErrorf(ErrEmptyTerm, -1, term.Value, "term normalizes to empty")
		}

		aliases := map[string]bool{normalized: true}
		exact := map[string]bool{}
		if resolver != nil {
			expansion, err := resolver.Expand(term.Value, cfg.Limits.MaxExpansionAliases)
			if err != nil {
				return CompiledQuery{}, err
			}
			for _, alias := range expansion.Aliases {
				aliases[alias] = true
			}
			for _, alias := range expansion.ExactAliases {
				exact[alias] = true
			}
			compiledTerm.Concepts = expansion.Concepts
		} else if concepts != nil {
			// Non-registry resolver: ask for the concept directly.
			if concept, ok := concepts.Resolve(term.Value); ok {
				compiledTerm.Concepts = []string{Normalize(concept.Name)}
				for _, alias := range concept.Aliases {
					if norm := Normalize(alias); norm != "" {
						aliases[norm] = true
					}
				}
				for _, alias := range concept.ExactAliases {
					if norm := Normalize(alias); norm != "" {
						aliases[norm] = true
						exact[norm] = true
					}
				}
			}
		}

		compiledTerm.Aliases = make([]string, 0, len(aliases))
		for alias := range aliases {
			compiledTerm.Aliases = append(compiledTerm.Aliases, alias)
		}
		sort.Strings(compiledTerm.Aliases)
		compiledTerm.ExactAliases = exact

		if term.Excluded {
			compiled.Negative = append(compiled.Negative, compiledTerm)
		} else {
			compiled.Positive = append(compiled.Positive, compiledTerm)
		}
	}

	return compiled, nil
}

// DebugString renders how the engine interpreted the query. It is meant for
// developers and admin tooling, not end users.
func (c CompiledQuery) DebugString() string {
	var builder strings.Builder
	builder.WriteString("query")
	if c.Raw != "" {
		fmt.Fprintf(&builder, " %q", c.Raw)
	}
	fmt.Fprintf(&builder, " [mode=%s missing=%s]", c.Mode, missingLabel(c.MissingFieldFails))
	for _, term := range c.Positive {
		builder.WriteString("\n  + ")
		writeTermDebug(&builder, term)
	}
	for _, term := range c.Negative {
		builder.WriteString("\n  - ")
		writeTermDebug(&builder, term)
	}
	for _, filter := range c.Filters {
		fmt.Fprintf(&builder, "\n  filter %s", filter.Explain())
	}
	return builder.String()
}

func writeTermDebug(builder *strings.Builder, term CompiledTerm) {
	kind := "ord"
	if term.Phrase {
		kind = "frase"
	}
	fmt.Fprintf(builder, "%s %q", kind, term.Original)
	if len(term.Concepts) > 0 {
		fmt.Fprintf(builder, " [koncept: %s]", strings.Join(term.Concepts, ", "))
	}
	fmt.Fprintf(builder, " -> %s", strings.Join(term.Aliases, ", "))
}

func missingLabel(missingFails bool) string {
	if missingFails {
		return "fail"
	}
	return "pass"
}
