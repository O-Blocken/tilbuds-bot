package search

import (
	"os"
	"testing"
)

func TestMergeConceptsReplacesInPlace(t *testing.T) {
	base := []Concept{
		{Name: "øl", Aliases: []string{"pilsner"}},
		{Name: "ost", Aliases: []string{"cheddar"}},
	}
	custom := []Concept{
		{Name: "øl", Aliases: []string{"hvedeøl"}, Description: "opdateret"},
	}
	merged := MergeConcepts(base, custom, nil)
	if len(merged) != 2 {
		t.Fatalf("len = %d, want 2", len(merged))
	}
	if merged[0].Name != "øl" || merged[0].Aliases[0] != "hvedeøl" || merged[0].Description != "opdateret" {
		t.Fatalf("replacement not applied in place: %+v", merged[0])
	}
	if merged[1].Name != "ost" || merged[1].Aliases[0] != "cheddar" {
		t.Fatalf("unrelated base concept damaged: %+v", merged[1])
	}
}

func TestMergeConceptsKeepsExactAliasesOnOverride(t *testing.T) {
	// /concepts set replaces aliases but cannot express exact aliases, so
	// the base exact mark must survive when the alias is kept.
	base := []Concept{{Name: "pålæg", Aliases: []string{"pålæg", "salami"}, ExactAliases: []string{"pålæg"}}}
	custom := []Concept{{Name: "pålæg", Aliases: []string{"pålæg", "salami", "skinke"}}}
	merged := MergeConcepts(base, custom, nil)
	if len(merged) != 1 {
		t.Fatalf("len = %d, want 1", len(merged))
	}
	if len(merged[0].ExactAliases) != 1 || merged[0].ExactAliases[0] != "pålæg" {
		t.Fatalf("exact aliases = %v, want [pålæg]", merged[0].ExactAliases)
	}

	// Dropping the alias drops the mark: it no longer applies.
	custom = []Concept{{Name: "pålæg", Aliases: []string{"salami"}}}
	merged = MergeConcepts(base, custom, nil)
	if len(merged[0].ExactAliases) != 0 {
		t.Fatalf("exact aliases = %v, want none", merged[0].ExactAliases)
	}
}

func TestMergeConceptsAppendsAndRemoves(t *testing.T) {
	base := []Concept{
		{Name: "øl"},
		{Name: "hemmelig"},
	}
	custom := []Concept{
		{Name: "snacks"},
	}
	merged := MergeConcepts(base, custom, []string{"hemmelig"})
	if len(merged) != 2 {
		t.Fatalf("len = %d, want 2 (øl + snacks): %+v", len(merged), merged)
	}
	if merged[0].Name != "øl" || merged[1].Name != "snacks" {
		t.Fatalf("order = %q, %q; want øl then appended snacks", merged[0].Name, merged[1].Name)
	}
}

func TestMergeConceptsRemovedBeatsCustom(t *testing.T) {
	merged := MergeConcepts(
		[]Concept{{Name: "øl"}},
		[]Concept{{Name: "øl", Aliases: []string{"nyt"}}},
		[]string{"øl"},
	)
	if len(merged) != 0 {
		t.Fatalf("tombstone must win over custom row: %+v", merged)
	}
}

func TestMergeConceptsNormalization(t *testing.T) {
	// "ØL" and "  øl  " must collapse into one concept: comparison is
	// normalized even though the display name keeps author spelling.
	merged := MergeConcepts(
		[]Concept{{Name: "ØL"}},
		[]Concept{{Name: "  øl  ", Aliases: []string{"x"}}},
		[]string{" Hemmelig "},
	)
	if len(merged) != 1 {
		t.Fatalf("names must normalize before compare: %+v", merged)
	}
	if merged[0].Name != "øl" {
		t.Fatalf("winner keeps trimmed author spelling, got %q", merged[0].Name)
	}
}

func TestBuildRegistryLayersOverrides(t *testing.T) {
	custom := []Concept{{Name: "helt nyt", Aliases: []string{"nyt"}}}
	registry, source, err := BuildRegistry("", custom, []string{"magnum"})
	if err != nil {
		t.Fatalf("BuildRegistry() error = %v", err)
	}
	if !source.Embedded {
		t.Fatalf("empty path must fall back to embedded, source = %+v", source)
	}
	if source.Custom != 1 || source.Removed != 1 {
		t.Fatalf("source counts = %+v", source)
	}
	if _, ok := registry.Resolve("helt nyt"); !ok {
		t.Fatal("custom concept missing from built registry")
	}
	if _, ok := registry.Resolve("magnum"); ok {
		t.Fatal("tombstoned concept still resolvable")
	}
	if _, ok := registry.Resolve("øl"); !ok {
		t.Fatal("embedded base concept missing")
	}
}

func TestLoadConceptsMissingFileFallsBackToEmbedded(t *testing.T) {
	concepts, source, err := LoadConcepts("testdata/does-not-exist.toml")
	if err != nil {
		t.Fatalf("LoadConcepts() error = %v", err)
	}
	if !source.Embedded || len(concepts) == 0 {
		t.Fatalf("fallback failed: source = %+v, %d concepts", source, len(concepts))
	}
}

func TestLoadConceptsFileWins(t *testing.T) {
	path := t.TempDir() + "/concepts.toml"
	data := []byte("[[concepts]]\nname = \"kun fil\"\naliases = [\"x\"]\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	concepts, source, err := LoadConcepts(path)
	if err != nil {
		t.Fatalf("LoadConcepts() error = %v", err)
	}
	if source.Embedded {
		t.Fatal("file-backed load must not report embedded")
	}
	if source.Path != path || len(concepts) != 1 || concepts[0].Name != "kun fil" {
		t.Fatalf("loaded %+v from %+v", concepts, source)
	}
}

func TestRegistryConceptsSorted(t *testing.T) {
	registry, err := NewRegistry([]Concept{{Name: "b"}, {Name: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	all := registry.Concepts()
	if len(all) != 2 || all[0].Name != "a" || all[1].Name != "b" {
		t.Fatalf("Concepts() = %+v, want sorted", all)
	}
}
