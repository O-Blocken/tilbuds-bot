package search

import (
	"errors"
	"strings"
	"testing"
)

// registry returns the default registry or fails the test.
func registry(t *testing.T) *Registry {
	t.Helper()
	r, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry() error: %v", err)
	}
	return r
}

// TestDefaultRegistryLoads is the runtime proof that the embedded
// concepts.toml exists and parses: a missing go:embed target fails at
// compile time, and a broken registry fails here.
func TestDefaultRegistryLoads(t *testing.T) {
	r := registry(t)
	concept, ok := r.Resolve("øl")
	if !ok {
		t.Fatal("concept øl missing from default registry")
	}
	if len(concept.Aliases) < 3 {
		t.Fatalf("concept øl has %d aliases, want >= 3", len(concept.Aliases))
	}
	if _, ok := r.Resolve("slik"); !ok {
		t.Fatal("concept slik missing from default registry")
	}
	if _, ok := r.Resolve("julekalender"); !ok {
		t.Fatal("concept julekalender missing from default registry")
	}
}

func TestParseConceptsTOML(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr bool
		check   func(t *testing.T, concepts []Concept)
	}{
		{
			name: "simple concept",
			data: "[[concepts]]\nname = \"øl\"\naliases = [\"pilsner\", \"lager\"]\n",
			check: func(t *testing.T, concepts []Concept) {
				if len(concepts) != 1 || concepts[0].Name != "øl" || len(concepts[0].Aliases) != 2 {
					t.Fatalf("unexpected parse: %+v", concepts)
				}
			},
		},
		{
			name: "multiline array with comment",
			data: "[[concepts]]\nname = \"a\"\naliases = [\n  \"x\", # first\n  \"y\",\n]\n",
			check: func(t *testing.T, concepts []Concept) {
				if len(concepts[0].Aliases) != 2 {
					t.Fatalf("aliases = %v", concepts[0].Aliases)
				}
			},
		},
		{
			name: "literal string and description",
			data: "[[concepts]]\nname = 'ost'\ndescription = 'note'\naliases = []\n",
			check: func(t *testing.T, concepts []Concept) {
				if concepts[0].Description != "note" {
					t.Fatalf("description = %q", concepts[0].Description)
				}
			},
		},
		{
			name: "two concepts with refs",
			data: "[[concepts]]\nname = \"a\"\naliases = [\"x\"]\n\n[[concepts]]\nname = \"b\"\naliases = [\"y\"]\nrefs = [\"a\"]\n",
			check: func(t *testing.T, concepts []Concept) {
				if len(concepts) != 2 || concepts[1].Refs[0] != "a" {
					t.Fatalf("unexpected: %+v", concepts)
				}
			},
		},
		{name: "unknown key", data: "[[concepts]]\nname = \"a\"\nbogus = \"x\"\n", wantErr: true},
		{name: "key before table", data: "name = \"a\"\n", wantErr: true},
		{name: "unsupported table", data: "[[other]]\n", wantErr: true},
		{name: "unterminated string", data: "[[concepts]]\nname = \"a\n", wantErr: true},
		{name: "unterminated array", data: "[[concepts]]\nname = \"a\"\naliases = [\"x\"\n", wantErr: true},
		{name: "missing equals", data: "[[concepts]]\nname \"a\"\n", wantErr: true},
		{name: "name as array", data: "[[concepts]]\nname = [\"a\", \"b\"]\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			concepts, err := ParseConcepts([]byte(tt.data))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", concepts)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseConcepts error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, concepts)
			}
		})
	}
}

func TestNewRegistryValidation(t *testing.T) {
	if _, err := NewRegistry([]Concept{{Name: "a"}, {Name: "a"}}); err == nil {
		t.Fatal("duplicate concept names must error")
	}
	if _, err := NewRegistry([]Concept{{Name: "a", Refs: []string{"missing"}}}); err == nil {
		t.Fatal("unknown reference must error")
	}
	if _, err := NewRegistry([]Concept{{Name: "  "}}); err == nil {
		t.Fatal("empty name must error")
	}
	// A term may legitimately appear in several concepts.
	r, err := NewRegistry([]Concept{
		{Name: "a", Aliases: []string{"x"}},
		{Name: "b", Aliases: []string{"x"}},
	})
	if err != nil {
		t.Fatalf("duplicate alias across concepts: %v", err)
	}
	exp, err := r.Expand("x", 0)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(exp.Concepts) != 2 {
		t.Fatalf("x should resolve through both concepts, got %v", exp.Concepts)
	}
}

func TestExpandLiteralTerm(t *testing.T) {
	r := registry(t)
	exp, err := r.Expand("gulerodsraclette", 0)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if exp.IsConcept() {
		t.Fatalf("unknown term must stay literal, got concepts %v", exp.Concepts)
	}
	if len(exp.Aliases) != 1 || exp.Aliases[0] != "gulerodsraclette" {
		t.Fatalf("aliases = %v", exp.Aliases)
	}
}

func TestExpandConcept(t *testing.T) {
	r := registry(t)
	exp, err := r.Expand("øl", 0)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if !exp.IsConcept() {
		t.Fatal("øl should resolve through its concept")
	}
	joined := strings.Join(exp.Aliases, " ")
	// The term itself plus configured aliases and referenced brands.
	for _, want := range []string{"oel", "pilsner", "lager", "carlsberg", "tuborg classic"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expansion of øl missing %q: %s", want, joined)
		}
	}
}

func TestExpandThroughAlias(t *testing.T) {
	// A query for an alias resolves to its concept and expands fully.
	r := registry(t)
	viaAlias, err := r.Expand("pilsner", 0)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	viaName, err := r.Expand("øl", 0)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	joined := strings.Join(viaAlias.Aliases, ",")
	if !strings.Contains(joined, "carlsberg") {
		t.Fatalf("alias expansion should include referenced brands: %s", joined)
	}
	if len(viaName.Aliases) != len(viaAlias.Aliases) {
		t.Fatalf("alias and concept name should expand equally: %d vs %d", len(viaName.Aliases), len(viaAlias.Aliases))
	}
}

func TestExpandCycleSafe(t *testing.T) {
	// By convention (see concepts.toml) a concept lists its own name in
	// Aliases when the name should be searchable through references.
	r, err := NewRegistry([]Concept{
		{Name: "a", Aliases: []string{"a", "x"}, Refs: []string{"b"}},
		{Name: "b", Aliases: []string{"b", "y"}, Refs: []string{"a"}},
	})
	if err != nil {
		t.Fatalf("cyclic refs must load (cycle-safe expansion): %v", err)
	}
	exp, err := r.Expand("a", 0)
	if err != nil {
		t.Fatalf("Expand on cyclic registry: %v", err)
	}
	joined := strings.Join(exp.Aliases, ",")
	for _, want := range []string{"x", "y", "a", "b"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("cycle expansion missing %q: %s", want, joined)
		}
	}
}

func TestExpandLimit(t *testing.T) {
	r := registry(t)
	_, err := r.Expand("slik", 2)
	if err == nil {
		t.Fatal("expansion over the alias cap must error")
	}
	var parseErr *ParseError
	if !errors.As(err, &parseErr) || parseErr.Code != ErrExpansionTooLarge {
		t.Fatalf("want ErrExpansionTooLarge, got %v", err)
	}
}
