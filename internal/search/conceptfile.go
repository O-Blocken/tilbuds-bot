package search

import (
	"fmt"
	"os"
	"strings"
)

// ConceptSource describes where concept data came from, for admin
// listings and diagnostics.
type ConceptSource struct {
	// Path is the concepts file that was loaded, or "" for the embedded
	// registry.
	Path string
	// Embedded is true when the shipped concepts.toml was used.
	Embedded bool
	// Custom is the count of persisted concept overrides applied.
	Custom int
	// Removed is the count of tombstoned concepts hidden.
	Removed int
}

// LoadConcepts reads the base concept layer: the file at path when it
// exists (Docker deployments mount one next to the config), else the
// embedded concepts.toml. It returns the base concepts and where they
// came from.
func LoadConcepts(path string) ([]Concept, ConceptSource, error) {
	source := ConceptSource{Path: strings.TrimSpace(path)}
	if source.Path != "" {
		data, err := os.ReadFile(source.Path)
		if err == nil {
			concepts, err := ParseConcepts(data)
			if err != nil {
				return nil, source, fmt.Errorf("parse %s: %w", source.Path, err)
			}
			return concepts, source, nil
		}
		if !os.IsNotExist(err) {
			return nil, source, fmt.Errorf("read %s: %w", source.Path, err)
		}
	}
	concepts, err := EmbeddedConcepts()
	if err != nil {
		return nil, source, err
	}
	source.Path = ""
	source.Embedded = true
	return concepts, source, nil
}

// BuildRegistry assembles the effective registry: base concepts from
// path (or embedded), layered with persisted custom concepts and
// tombstones, validated as one registry.
func BuildRegistry(path string, custom []Concept, removed []string) (*Registry, ConceptSource, error) {
	base, source, err := LoadConcepts(path)
	if err != nil {
		return nil, source, err
	}
	merged := MergeConcepts(base, custom, removed)
	registry, err := NewRegistry(merged)
	if err != nil {
		return nil, source, err
	}
	source.Custom = len(custom)
	source.Removed = len(removed)
	return registry, source, nil
}
