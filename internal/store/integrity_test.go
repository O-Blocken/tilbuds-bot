package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func countRows(t *testing.T, db *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	return n
}

// TestForeignKeysEnabled is the regression test for the schema's ON DELETE
// CASCADE, which never fired while the pragma was off.
func TestForeignKeysEnabled(t *testing.T) {
	db := openTestStore(t)
	var enabled int
	if err := db.db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatal("foreign_keys pragma must be enabled on the connection")
	}
}

// TestDeleteWantRemovesTerms covers both the explicit delete and the cascade.
func TestDeleteWantRemovesTerms(t *testing.T) {
	db := openTestStore(t)
	if err := db.UpsertWant(WantRow{ID: "cola", Includes: []string{"coca cola"}, Excludes: []string{"zero"}}); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM want_terms WHERE want_id = ?`, "cola"); got != 2 {
		t.Fatalf("terms before delete = %d, want 2", got)
	}

	removed, err := db.DeleteWant("cola")
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("DeleteWant() reported no deletion")
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM want_terms`); got != 0 {
		t.Fatalf("orphan want_terms after delete = %d, want 0", got)
	}

	// Deleting again reports nothing removed and stays clean.
	removed, err = db.DeleteWant("cola")
	if err != nil || removed {
		t.Fatalf("second DeleteWant() = %v, %v; want false, nil", removed, err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM want_terms`); got != 0 {
		t.Fatalf("orphan want_terms after second delete = %d", got)
	}
}

// TestOpenCleansLegacyOrphanTerms covers databases written by older versions
// that relied on a cascade which never ran. The orphan is inserted through a
// foreign-key-free connection, which is exactly how those rows were created.
func TestOpenCleansLegacyOrphanTerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO want_terms(want_id, term, kind) VALUES('ghost', 'cola', 'include')`); err != nil {
		legacy.Close()
		t.Fatalf("legacy insert failed: %v", err)
	}
	legacy.Close()

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer reopened.Close()
	if got := countRows(t, reopened, `SELECT COUNT(*) FROM want_terms`); got != 0 {
		t.Fatalf("orphan terms survived reopen = %d, want 0", got)
	}
}

func TestPruneHistoryDropsExpiredRows(t *testing.T) {
	db := openTestStore(t)
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	// One fresh and one expired observation for the same product.
	if _, err := db.db.Exec(`INSERT INTO price_observations(norm_key, retailer, price, observed_at, publication_id)
		VALUES('cola', 'spar', 10, ?, 'pub-new')`, now.Add(-24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO price_observations(norm_key, retailer, price, observed_at, publication_id)
		VALUES('cola', 'spar', 12, ?, 'pub-old')`, now.Add(-PriceHistoryRetention-24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	// One fresh and one expired prej verdict.
	if err := db.PutPrejVerdict("pub-new", "key", "verdict"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE prej_cache SET fetched_at = ? WHERE publication_id = 'pub-new'`,
		now.Add(-time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO prej_cache(publication_id, norm_key, verdict, fetched_at)
		VALUES('pub-old', 'key', 'verdict', ?)`, now.Add(-PrejCacheRetention-time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	stats, err := db.PruneHistory(now)
	if err != nil {
		t.Fatalf("PruneHistory() error = %v", err)
	}
	if stats.Observations != 1 || stats.PrejCached != 1 {
		t.Fatalf("stats = %+v, want one observation and one cached verdict", stats)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM price_observations`); got != 1 {
		t.Errorf("observations after prune = %d, want 1", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM prej_cache`); got != 1 {
		t.Errorf("prej cache after prune = %d, want 1", got)
	}
}

func TestPruneHistoryDropsSupersededPostedDeals(t *testing.T) {
	db := openTestStore(t)
	if err := db.PutFeed(FeedState{Retailer: "spar", ChannelID: 1, LastPublicationID: "pub-new"}); err != nil {
		t.Fatal(err)
	}
	if err := db.AddPostedDeals("spar", "pub-new", []string{"offer-1"}); err != nil {
		t.Fatal(err)
	}
	if err := db.AddPostedDeals("spar", "pub-old", []string{"offer-0"}); err != nil {
		t.Fatal(err)
	}
	if err := db.AddPostedDeals("fotex", "pub-old-fotex", []string{"offer-0"}); err != nil {
		t.Fatal(err)
	}

	stats, err := db.PruneHistory(time.Now())
	if err != nil {
		t.Fatalf("PruneHistory() error = %v", err)
	}
	// spar keeps its current publication; fotex has no feeds row at all, so its
	// keys are not referenced by a feed and are removed as well.
	if stats.PostedDeals != 2 {
		t.Fatalf("posted deals pruned = %d, want 2", stats.PostedDeals)
	}
	keys, err := db.PostedKeys("spar", "pub-new")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || !keys["offer-1"] {
		t.Fatalf("current publication keys = %v, want offer-1", keys)
	}
}

func TestDSNWithPragmas(t *testing.T) {
	if got := dsnWithPragmas("bot.db"); got != "bot.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" {
		t.Errorf("dsnWithPragmas() = %q", got)
	}
	existing := "file:bot.db?cache=shared"
	if got := dsnWithPragmas(existing); got != existing {
		t.Errorf("dsnWithPragmas() rewrote an existing DSN: %q", got)
	}
}
