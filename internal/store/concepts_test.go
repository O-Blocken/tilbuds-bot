package store

import (
	"path/filepath"
	"testing"
)

func conceptTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestConceptOverridesRoundTrip(t *testing.T) {
	db := conceptTestStore(t)

	if err := db.UpsertConcept(ConceptRow{
		Name:        "øl",
		Description: "egne øl-typer",
		Aliases:     []string{"pilsner", "hvedeøl"},
		Refs:        []string{"carlsberg"},
	}); err != nil {
		t.Fatalf("UpsertConcept() error = %v", err)
	}
	if err := db.UpsertConcept(ConceptRow{Name: "hemmelig", Deleted: true}); err != nil {
		t.Fatalf("UpsertConcept(tombstone) error = %v", err)
	}

	rows, err := db.ListConcepts()
	if err != nil {
		t.Fatalf("ListConcepts() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListConcepts() = %d rows, want 2", len(rows))
	}

	byName := map[string]ConceptRow{}
	for _, row := range rows {
		byName[row.Name] = row
	}
	ol := byName["øl"]
	if ol.Description != "egne øl-typer" || len(ol.Aliases) != 2 || ol.Aliases[1] != "hvedeøl" {
		t.Fatalf("øl row = %+v", ol)
	}
	if len(ol.Refs) != 1 || ol.Refs[0] != "carlsberg" {
		t.Fatalf("øl refs = %v", ol.Refs)
	}
	if ol.Deleted {
		t.Fatal("øl should not be tombstoned")
	}
	if !byName["hemmelig"].Deleted {
		t.Fatal("hemmelig should be a tombstone")
	}

	// Update replaces in place.
	if err := db.UpsertConcept(ConceptRow{Name: "øl", Aliases: []string{"pilsner"}}); err != nil {
		t.Fatalf("UpsertConcept(update) error = %v", err)
	}
	rows, err = db.ListConcepts()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("after update: %d rows, want 2", len(rows))
	}
	for _, row := range rows {
		if row.Name == "øl" && len(row.Aliases) != 1 {
			t.Fatalf("aliases not replaced: %v", row.Aliases)
		}
	}

	// DeleteConcept drops the override entirely (restore-to-toml).
	removed, err := db.DeleteConcept("øl")
	if err != nil || !removed {
		t.Fatalf("DeleteConcept() = %v, %v; want true", removed, err)
	}
	removed, err = db.DeleteConcept("øl")
	if err != nil || removed {
		t.Fatalf("DeleteConcept(again) = %v, %v; want false", removed, err)
	}
}

func TestConceptUpsertValidation(t *testing.T) {
	db := conceptTestStore(t)
	if err := db.UpsertConcept(ConceptRow{Name: "  "}); err == nil {
		t.Fatal("empty name must be rejected")
	}
}

func TestPriceAlertsRoundTrip(t *testing.T) {
	db := conceptTestStore(t)

	target := 12.5
	id, err := db.AddPriceAlert(PriceAlertRow{
		Query:       "coca cola -zero",
		TargetPrice: &target,
		Retailer:    "fotex",
		ChannelID:   42,
		UserID:      "user-1",
	})
	if err != nil {
		t.Fatalf("AddPriceAlert() error = %v", err)
	}
	if id <= 0 {
		t.Fatalf("AddPriceAlert() id = %d, want > 0", id)
	}
	if _, err := db.AddPriceAlert(PriceAlertRow{Query: "øl", ChannelID: 42}); err != nil {
		t.Fatalf("AddPriceAlert(all-time-low) error = %v", err)
	}

	rows, err := db.ListPriceAlerts()
	if err != nil {
		t.Fatalf("ListPriceAlerts() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListPriceAlerts() = %d rows, want 2", len(rows))
	}
	first := rows[0]
	if first.Query != "coca cola -zero" || first.TargetPrice == nil || *first.TargetPrice != 12.5 {
		t.Fatalf("first alert = %+v", first)
	}
	if first.Retailer != "fotex" || first.ChannelID != 42 || first.UserID != "user-1" {
		t.Fatalf("first alert metadata = %+v", first)
	}
	if !first.Active || first.FiredPublication != "" {
		t.Fatalf("first alert state = active %v, fired %q", first.Active, first.FiredPublication)
	}
	if rows[1].TargetPrice != nil {
		t.Fatalf("all-time-low alert has target: %+v", rows[1])
	}

	if err := db.MarkPriceAlertFired(first.ID, "pub-1"); err != nil {
		t.Fatalf("MarkPriceAlertFired() error = %v", err)
	}
	rows, err = db.ListPriceAlerts()
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].FiredPublication != "pub-1" {
		t.Fatalf("fired publication = %q", rows[0].FiredPublication)
	}
	if !rows[1].Active {
		t.Fatal("second alert should still be active")
	}

	removed, err := db.DeletePriceAlert(first.ID)
	if err != nil || !removed {
		t.Fatalf("DeletePriceAlert() = %v, %v", removed, err)
	}
	removed, err = db.DeletePriceAlert(first.ID)
	if err != nil || removed {
		t.Fatalf("DeletePriceAlert(again) = %v, %v", removed, err)
	}
}

func TestAddPriceAlertValidation(t *testing.T) {
	db := conceptTestStore(t)
	if _, err := db.AddPriceAlert(PriceAlertRow{Query: " "}); err == nil {
		t.Fatal("empty query must be rejected")
	}
}

func TestCheapestOutsideExcludesCurrentPublication(t *testing.T) {
	db := conceptTestStore(t)

	observations := []PriceObservation{
		{NormKey: "coca cola", Price: 20, PublicationID: "pub-old", Source: "hotspot"},
		{NormKey: "coca cola", Price: 15, PublicationID: "pub-old-2", Source: "hotspot"},
		{NormKey: "coca cola", Price: 9, PublicationID: "pub-now", Source: "hotspot"},
	}
	if err := db.RecordObservations("spar", observations); err != nil {
		t.Fatalf("RecordObservations() error = %v", err)
	}

	cheapest, n, ok, err := db.CheapestOutside("spar", "coca cola", "pub-now")
	if err != nil || !ok {
		t.Fatalf("CheapestOutside() = %v, %v, %v", cheapest, ok, err)
	}
	if cheapest != 15 {
		t.Fatalf("cheapest outside = %v, want 15 (current publication excluded)", cheapest)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}

	// Wrong retailer sees nothing.
	if _, _, ok, err := db.CheapestOutside("fotex", "coca cola", "pub-now"); err != nil || ok {
		t.Fatalf("CheapestOutside(other retailer) ok = %v, %v", ok, err)
	}

	// Excluding the oldest publication still sees the other two (9 is the
	// cheapest of pub-old-2 and pub-now).
	cheapest, _, ok, err = db.CheapestOutside("spar", "coca cola", "pub-old")
	if err != nil || !ok {
		t.Fatalf("CheapestOutside(pub-old) ok = %v, %v", ok, err)
	}
	if cheapest != 9 {
		t.Fatalf("cheapest outside pub-old = %v, want 9", cheapest)
	}

	// A product seen only in the current publication has no history.
	if _, _, ok, _ := db.CheapestOutside("spar", "nyt produkt", "pub-now"); ok {
		t.Fatal("unseen product must report no history")
	}
}

func TestStatsCountsNewTables(t *testing.T) {
	db := conceptTestStore(t)
	if err := db.UpsertConcept(ConceptRow{Name: "x", Aliases: []string{"y"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPriceAlert(PriceAlertRow{Query: "x"}); err != nil {
		t.Fatal(err)
	}
	stats, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats.Concepts != 1 || stats.Alerts != 1 {
		t.Fatalf("Stats() concepts=%d alerts=%d, want 1 and 1", stats.Concepts, stats.Alerts)
	}
}
