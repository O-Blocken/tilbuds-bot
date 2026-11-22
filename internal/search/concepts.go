package search

import (
	_ "embed"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Concept maps a user-facing term to known alternative spellings,
// synonyms, brand families and product-type wording.
// Concepts are data: adding one never requires matcher changes.
type Concept struct {
	// Name is the concept's canonical term, e.g. "øl".
	Name string `json:"name"`
	// Description is an optional human note for documentation tooling.
	Description string `json:"description,omitempty"`
	// Aliases are the accepted alternatives, e.g. "pilsner", "lager".
	// Aliases may be single words or phrases.
	Aliases []string `json:"aliases"`
	// ExactAliases are aliases that must match whole words only and never
	// inside compound tokens. Use them for ambiguous stems: "pålæg" must
	// not match "chokopålæg" or "pålægsmaskine". Exact aliases are also
	// added to Aliases automatically.
	ExactAliases []string `json:"exact_aliases,omitempty"`
	// Refs name other concepts whose aliases also belong to this concept,
	// e.g. a "sodavand" concept referencing the "cola" brand concept.
	// References may form cycles; expansion is cycle-safe.
	Refs []string `json:"refs,omitempty"`
}

// ConceptResolver resolves a normalized term to a concept. The matcher
// depends only on this interface, so concepts may come from static data,
// JSON/TOML files, embedded resources or persistence.
type ConceptResolver interface {
	// Resolve returns the concept registered under the given normalized
	// name. Alias hits resolve through Expand instead.
	Resolve(term string) (Concept, bool)
}

// Expansion is the deterministic result of expanding one term through the
// concept registry.
type Expansion struct {
	// Term is the normalized input term.
	Term string
	// Concepts lists the concept names the term resolved through,
	// sorted. Empty for literal terms.
	Concepts []string
	// Aliases are all normalized search alternatives: the term itself
	// plus every alias of the resolved concepts and their transitive
	// references. Sorted, deduplicated.
	Aliases []string
	// ExactAliases is the subset of Aliases that must not match inside
	// compound tokens. Sorted, deduplicated.
	ExactAliases []string
}

// IsConcept reports whether the expansion passed through any concept.
func (e Expansion) IsConcept() bool { return len(e.Concepts) > 0 }

// Registry is an in-memory concept registry with normalized lookup maps
// built once at load time.
type Registry struct {
	byName map[string]Concept  // normalized concept name -> concept
	byTerm map[string][]string // normalized term -> concept names (aliases)
}

// NewRegistry validates and indexes concepts. Names and aliases are
// normalized at load time. Duplicate concept names are an error; a term
// may legitimately appear as an alias in several concepts (expansion then
// includes all of them).
func NewRegistry(concepts []Concept) (*Registry, error) {
	r := &Registry{
		byName: make(map[string]Concept, len(concepts)),
		byTerm: make(map[string][]string, len(concepts)),
	}
	for _, concept := range concepts {
		name := Normalize(concept.Name)
		if name == "" {
			return nil, fmt.Errorf("concept with empty name")
		}
		if _, exists := r.byName[name]; exists {
			return nil, fmt.Errorf("duplicate concept %q", concept.Name)
		}
		normalized := Concept{Name: name, Refs: append([]string(nil), concept.Refs...)}
		exactSet := map[string]bool{}
		for _, alias := range concept.ExactAliases {
			if norm := Normalize(alias); norm != "" {
				exactSet[norm] = true
			}
		}
		aliases := make([]string, 0, len(concept.Aliases)+len(exactSet))
		seen := map[string]bool{}
		for _, alias := range append(append([]string(nil), concept.Aliases...), concept.ExactAliases...) {
			alias = Normalize(alias)
			if alias == "" || seen[alias] {
				continue
			}
			seen[alias] = true
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		normalized.Aliases = aliases
		normalized.ExactAliases = make([]string, 0, len(exactSet))
		for alias := range exactSet {
			normalized.ExactAliases = append(normalized.ExactAliases, alias)
		}
		sort.Strings(normalized.ExactAliases)
		r.byName[name] = normalized
		r.byTerm[name] = append(r.byTerm[name], name)
		for _, alias := range aliases {
			if alias == name {
				// Already registered above; keeping the name in Aliases is
				// what lets referencing concepts match it.
				continue
			}
			r.byTerm[alias] = append(r.byTerm[alias], name)
		}
	}
	// Validate references up front so typos surface at load, not at match.
	for name, concept := range r.byName {
		for _, ref := range concept.Refs {
			if _, ok := r.byName[Normalize(ref)]; !ok {
				return nil, fmt.Errorf("concept %q references unknown concept %q", name, ref)
			}
		}
	}
	for term := range r.byTerm {
		sort.Strings(r.byTerm[term])
	}
	return r, nil
}

// Resolve implements ConceptResolver.
func (r *Registry) Resolve(term string) (Concept, bool) {
	concept, ok := r.byName[Normalize(term)]
	return concept, ok
}

// Expand resolves a term through the registry and returns the
// deterministic set of normalized search alternatives. A term with no
// concept expands to itself. When a term is an alias of one or more
// concepts (or is a concept name), expansion includes every alias of
// those concepts and, transitively, their references. Reference cycles
// are broken by visited-set, and expansion is capped by maxAliases
// (> 0); exceeding the cap returns ErrExpansionTooLarge.
//
// The original term is always part of the alias set, so a query for
// "tuborg" still matches literal "Tuborg" even when a concept adds
// further alternatives.
func (r *Registry) Expand(term string, maxAliases int) (Expansion, error) {
	normalized := Normalize(term)
	expansion := Expansion{Term: normalized, Aliases: []string{normalized}}
	if r == nil {
		sort.Strings(expansion.Aliases)
		return expansion, nil
	}

	names := r.conceptsFor(normalized)
	expansion.Concepts = names

	aliasSet := map[string]bool{normalized: true}
	exactSet := map[string]bool{}
	visited := map[string]bool{}
	queue := append([]string(nil), names...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if visited[name] {
			continue // cycle guard
		}
		visited[name] = true
		concept, ok := r.byName[name]
		if !ok {
			continue
		}
		for _, alias := range concept.Aliases {
			aliasSet[alias] = true
		}
		for _, alias := range concept.ExactAliases {
			exactSet[alias] = true
		}
		for _, ref := range concept.Refs {
			refName := Normalize(ref)
			if !visited[refName] {
				queue = append(queue, refName)
			}
		}
	}

	if maxAliases > 0 && len(aliasSet) > maxAliases {
		return Expansion{}, parseErrorf(ErrExpansionTooLarge, -1, term,
			"term %q expands to %d aliases, limit is %d", term, len(aliasSet), maxAliases)
	}
	expansion.Aliases = expansion.Aliases[:0]
	for alias := range aliasSet {
		expansion.Aliases = append(expansion.Aliases, alias)
	}
	sort.Strings(expansion.Aliases)
	for alias := range exactSet {
		// An exact alias only counts when it is part of this expansion.
		if aliasSet[alias] {
			expansion.ExactAliases = append(expansion.ExactAliases, alias)
		}
	}
	sort.Strings(expansion.ExactAliases)
	return expansion, nil
}

// conceptsFor returns the sorted concept names a term belongs to: exact
// concept names first (a name resolves only to itself), then every
// concept listing the term as an alias.
func (r *Registry) conceptsFor(normalized string) []string {
	if names, ok := r.byTerm[normalized]; ok {
		out := append([]string(nil), names...)
		sort.Strings(out)
		return out
	}
	return nil
}

// defaultConceptsTOML is the embedded concept registry.
//
//go:embed concepts.toml
var defaultConceptsTOML []byte

// DefaultRegistry parses and indexes the embedded concepts.toml registry.
func DefaultRegistry() (*Registry, error) {
	concepts, err := ParseConcepts(defaultConceptsTOML)
	if err != nil {
		return nil, fmt.Errorf("parse embedded concepts: %w", err)
	}
	return NewRegistry(concepts)
}

// LoadRegistry reads a TOML concept registry from r and indexes it.
func LoadRegistry(r io.Reader) (*Registry, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read concepts: %w", err)
	}
	concepts, err := ParseConcepts(data)
	if err != nil {
		return nil, err
	}
	return NewRegistry(concepts)
}

// Joined renders the expansion as a debug string.
func (e Expansion) Joined() string {
	return strings.Join(e.Aliases, ", ")
}

// EmbeddedConcepts returns the parsed concepts.toml shipped inside the
// binary. It is the base layer for MergeConcepts when no override file is
// mounted (Docker deployments can mount one next to the config).
func EmbeddedConcepts() ([]Concept, error) {
	return ParseConcepts(defaultConceptsTOML)
}

// Concepts returns every registered concept sorted by name, for admin
// listings and diagnostics.
func (r *Registry) Concepts() []Concept {
	out := make([]Concept, 0, len(r.byName))
	for _, concept := range r.byName {
		out = append(out, concept)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// MergeConcepts layers custom concept data over base data: concepts may come
// from embedded resources, configuration files or persistence. A custom concept
// with the same name replaces the base
// concept in place; custom concepts not in base are appended in input
// order; names listed in removed are dropped entirely. References to
// removed concepts are pruned from every surviving concept so hiding one
// concept never breaks the concepts that referenced it. The result is
// deterministic for identical inputs.
func MergeConcepts(base, custom []Concept, removed []string) []Concept {
	isRemoved := make(map[string]bool, len(removed))
	for _, name := range removed {
		if norm := Normalize(name); norm != "" {
			isRemoved[norm] = true
		}
	}
	customBy := make(map[string]Concept, len(custom))
	customOrder := make([]string, 0, len(custom))
	for _, concept := range custom {
		norm := Normalize(concept.Name)
		if norm == "" {
			continue
		}
		// The display name keeps the author's spelling; the normalized
		// name is only the comparison key.
		concept.Name = strings.TrimSpace(concept.Name)
		if _, seen := customBy[norm]; !seen {
			customOrder = append(customOrder, norm)
		}
		customBy[norm] = concept
	}

	merged := make([]Concept, 0, len(base)+len(custom))
	usedCustom := make(map[string]bool, len(custom))
	for _, concept := range base {
		norm := Normalize(concept.Name)
		if isRemoved[norm] {
			continue
		}
		if replacement, ok := customBy[norm]; ok {
			replacement.ExactAliases = preserveExactAliases(concept.ExactAliases, replacement)
			merged = append(merged, replacement)
			usedCustom[norm] = true
			continue
		}
		merged = append(merged, concept)
	}
	for _, norm := range customOrder {
		if usedCustom[norm] || isRemoved[norm] {
			continue
		}
		merged = append(merged, customBy[norm])
	}
	// A removed concept takes its references with it: drop refs pointing
	// at tombstoned names so hiding one concept never breaks the
	// concepts that referenced it. Genuine typos (refs to names that
	// never existed) still fail registry validation.
	if len(isRemoved) > 0 {
		for i := range merged {
			merged[i].Refs = pruneRemovedRefs(merged[i].Refs, isRemoved)
		}
	}
	return merged
}

// preserveExactAliases keeps a base concept's exact-match marks when an
// admin override replaces its aliases: /concepts set does not expose exact
// aliases, so dropping them silently would reintroduce compound false
// positives like chokopålæg for "pålæg". Base exact aliases survive only
// when the override still contains that alias.
func preserveExactAliases(baseExact []string, replacement Concept) []string {
	if len(baseExact) == 0 {
		return replacement.ExactAliases
	}
	present := make(map[string]bool, len(replacement.Aliases)+len(replacement.ExactAliases))
	for _, alias := range replacement.Aliases {
		present[Normalize(alias)] = true
	}
	for _, alias := range replacement.ExactAliases {
		present[Normalize(alias)] = true
	}
	exact := append([]string(nil), replacement.ExactAliases...)
	seen := make(map[string]bool, len(exact))
	for _, alias := range exact {
		seen[Normalize(alias)] = true
	}
	for _, alias := range baseExact {
		norm := Normalize(alias)
		if norm != "" && present[norm] && !seen[norm] {
			exact = append(exact, alias)
			seen[norm] = true
		}
	}
	return exact
}

// pruneRemovedRefs filters out references whose normalized name is
// removed, preserving the remaining references in order.
func pruneRemovedRefs(refs []string, isRemoved map[string]bool) []string {
	kept := refs[:0]
	for _, ref := range refs {
		if isRemoved[Normalize(ref)] {
			continue
		}
		kept = append(kept, ref)
	}
	return kept
}
