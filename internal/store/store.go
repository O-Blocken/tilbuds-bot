// Package store persists bot state in SQLite: shared wants, price
// observations, prej.dk verdict cache, embedding cache, per-retailer feed
// state and posted deal keys. The driver (modernc.org/sqlite) is pure Go,
// so the store works in every build including CGO-free ones.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"spar-tilbuds-bot/internal/match"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS wants (
	id TEXT PRIMARY KEY,
	describe TEXT NOT NULL DEFAULT '',
	threshold REAL NOT NULL DEFAULT 0,
	max_price REAL NULL,
	max_unit_price REAL NULL,
	allow_app_price INTEGER NULL,
	retailers TEXT NOT NULL DEFAULT '[]',
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS want_terms (
	want_id TEXT NOT NULL REFERENCES wants(id) ON DELETE CASCADE,
	term TEXT NOT NULL,
	kind TEXT NOT NULL,
	PRIMARY KEY (want_id, term, kind)
);
CREATE TABLE IF NOT EXISTS price_observations (
	norm_key TEXT NOT NULL,
	retailer TEXT NOT NULL,
	price REAL NOT NULL,
	unit_price REAL NULL,
	observed_at TEXT NOT NULL,
	publication_id TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT 'hotspot'
);
CREATE INDEX IF NOT EXISTS idx_price_obs_key ON price_observations(norm_key, retailer, observed_at);
CREATE TABLE IF NOT EXISTS prej_cache (
	publication_id TEXT NOT NULL,
	norm_key TEXT NOT NULL,
	verdict TEXT NOT NULL,
	fetched_at TEXT NOT NULL,
	PRIMARY KEY (publication_id, norm_key)
);
CREATE TABLE IF NOT EXISTS feeds (
	retailer TEXT PRIMARY KEY,
	channel_id INTEGER NOT NULL DEFAULT 0,
	last_publication_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS posted_deals (
	retailer TEXT NOT NULL,
	publication_id TEXT NOT NULL,
	offer_key TEXT NOT NULL,
	PRIMARY KEY (retailer, publication_id, offer_key)
);
CREATE TABLE IF NOT EXISTS concepts (
	name TEXT PRIMARY KEY,
	description TEXT NOT NULL DEFAULT '',
	aliases TEXT NOT NULL DEFAULT '[]',
	refs TEXT NOT NULL DEFAULT '[]',
	deleted INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS price_alerts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	want_id TEXT NOT NULL DEFAULT '',
	query TEXT NOT NULL,
	target_price REAL NULL,
	retailer TEXT NOT NULL DEFAULT '',
	channel_id INTEGER NOT NULL DEFAULT 0,
	user_id TEXT NOT NULL DEFAULT '',
	active INTEGER NOT NULL DEFAULT 1,
	fired_publication TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
`

// Store is a SQLite-backed bot state database.
type Store struct {
	db   *sql.DB
	path string
}

// Open creates parent directories, opens (or creates) the database, applies
// the schema and clears rows orphaned by older versions.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("store path is empty")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create store dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dsnWithPragmas(path))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate store: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("wal mode: %w", err)
	}
	// Terms whose want was deleted by an older version that did not rely on
	// the foreign key: clean them once at startup.
	if _, err := db.Exec(`DELETE FROM want_terms WHERE want_id NOT IN (SELECT id FROM wants)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("clean orphan want terms: %w", err)
	}
	return &Store{db: db, path: path}, nil
}

// dsnWithPragmas appends the connection pragmas this store depends on.
//
// Pragmas are per-connection in SQLite and database/sql may open a fresh
// connection later, so they belong in the DSN instead of a one-off Exec. The
// schema relies on foreign_keys for ON DELETE CASCADE, and busy_timeout keeps a
// transient lock from failing a command outright.
func dsnWithPragmas(path string) string {
	if strings.ContainsAny(path, "?#") {
		// The caller already supplied a DSN; deleting a want still removes its
		// terms explicitly, so behaviour does not depend on the pragma.
		return path
	}
	return path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
}

// Close releases the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// WantRow is one stored want with its terms.
type WantRow struct {
	ID       string
	Describe string
	// Query is an optional raw query-language expression; when set it
	// takes precedence over Includes/Excludes.
	Query         string
	Threshold     float64
	MaxPrice      *float64
	MaxUnitPrice  *float64
	AllowAppPrice *bool
	Retailers     []string
	Includes      []string
	Excludes      []string
}

// ToMatchWant converts a row to a matcher want.
func (row WantRow) ToMatchWant() match.Want {
	return match.Want{
		ID:            row.ID,
		Describe:      row.Describe,
		Query:         row.Query,
		Include:       append([]string(nil), row.Includes...),
		Exclude:       append([]string(nil), row.Excludes...),
		MaxPrice:      row.MaxPrice,
		MaxUnitPrice:  row.MaxUnitPrice,
		AllowAppPrice: row.AllowAppPrice,
		Threshold:     row.Threshold,
		Retailers:     append([]string(nil), row.Retailers...),
	}
}

// ToMatchWants converts rows to matcher wants in stored order.
func ToMatchWants(rows []WantRow) []match.Want {
	wants := make([]match.Want, 0, len(rows))
	for _, row := range rows {
		wants = append(wants, row.ToMatchWant())
	}
	return wants
}

// RowFromMatchWant converts a matcher want to a storable row.
func RowFromMatchWant(want match.Want) WantRow {
	return WantRow{
		ID:            want.ID,
		Describe:      want.Describe,
		Query:         want.Query,
		Threshold:     want.Threshold,
		MaxPrice:      want.MaxPrice,
		MaxUnitPrice:  want.MaxUnitPrice,
		AllowAppPrice: want.AllowAppPrice,
		Retailers:     append([]string(nil), want.Retailers...),
		Includes:      append([]string(nil), want.Include...),
		Excludes:      append([]string(nil), want.Exclude...),
	}
}

// ListWants returns all wants ordered by id.
func (s *Store) ListWants() ([]WantRow, error) {
	rows, err := s.db.Query(`SELECT id, describe, threshold, max_price, max_unit_price, allow_app_price, retailers FROM wants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WantRow
	for rows.Next() {
		var row WantRow
		var maxPrice, maxUnitPrice sql.NullFloat64
		var allowApp sql.NullBool
		var retailers string
		if err := rows.Scan(&row.ID, &row.Describe, &row.Threshold, &maxPrice, &maxUnitPrice, &allowApp, &retailers); err != nil {
			return nil, err
		}
		if maxPrice.Valid {
			v := maxPrice.Float64
			row.MaxPrice = &v
		}
		if maxUnitPrice.Valid {
			v := maxUnitPrice.Float64
			row.MaxUnitPrice = &v
		}
		if allowApp.Valid {
			v := allowApp.Bool
			row.AllowAppPrice = &v
		}
		row.Retailers = decodeStringList(retailers)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	terms, err := s.listTerms()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Includes = terms[out[i].ID]["include"]
		out[i].Excludes = terms[out[i].ID]["exclude"]
		if queries := terms[out[i].ID]["query"]; len(queries) > 0 {
			out[i].Query = queries[0]
		}
	}
	return out, nil
}

func (s *Store) listTerms() (map[string]map[string][]string, error) {
	rows, err := s.db.Query(`SELECT want_id, term, kind FROM want_terms ORDER BY want_id, kind, term`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string][]string{}
	for rows.Next() {
		var id, term, kind string
		if err := rows.Scan(&id, &term, &kind); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string][]string{}
		}
		out[id][kind] = append(out[id][kind], term)
	}
	return out, rows.Err()
}

// UpsertWant inserts or replaces a want and its terms.
func (s *Store) UpsertWant(row WantRow) error {
	if strings.TrimSpace(row.ID) == "" {
		return fmt.Errorf("want id is empty")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var maxPrice, maxUnitPrice any
	if row.MaxPrice != nil {
		maxPrice = *row.MaxPrice
	}
	if row.MaxUnitPrice != nil {
		maxUnitPrice = *row.MaxUnitPrice
	}
	var allowApp any
	if row.AllowAppPrice != nil {
		if *row.AllowAppPrice {
			allowApp = 1
		} else {
			allowApp = 0
		}
	}
	retailers := encodeStringList(nonNilStrings(row.Retailers))
	_, err = tx.Exec(`INSERT INTO wants(id, describe, threshold, max_price, max_unit_price, allow_app_price, retailers, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET
		describe=excluded.describe, threshold=excluded.threshold, max_price=excluded.max_price,
		max_unit_price=excluded.max_unit_price, allow_app_price=excluded.allow_app_price,
		retailers=excluded.retailers, updated_at=excluded.updated_at`,
		row.ID, row.Describe, row.Threshold, maxPrice, maxUnitPrice, allowApp, retailers, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM want_terms WHERE want_id = ?`, row.ID); err != nil {
		return err
	}
	if expr := strings.TrimSpace(row.Query); expr != "" {
		if _, err := tx.Exec(`INSERT INTO want_terms(want_id, term, kind) VALUES(?, ?, 'query')`, row.ID, expr); err != nil {
			return err
		}
	}
	for _, term := range row.Includes {
		if strings.TrimSpace(term) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO want_terms(want_id, term, kind) VALUES(?, ?, 'include')`, row.ID, term); err != nil {
			return err
		}
	}
	for _, term := range row.Excludes {
		if strings.TrimSpace(term) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO want_terms(want_id, term, kind) VALUES(?, ?, 'exclude')`, row.ID, term); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteWant removes a want and its terms. It reports whether one existed.
//
// Terms are deleted explicitly rather than by relying on ON DELETE CASCADE, so
// the result does not depend on the foreign_keys pragma being enabled on the
// connection that serves the statement.
func (s *Store) DeleteWant(id string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`DELETE FROM wants WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM want_terms WHERE want_id = ?`, id); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}

// WantsEmpty reports whether no wants are stored (fresh database).
func (s *Store) WantsEmpty() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM wants`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// ImportMatchWants seeds the store from TOML wants. Existing rows win:
// only ids absent from the store are inserted.
func (s *Store) ImportMatchWants(wants []match.Want) (int, error) {
	existing := map[string]bool{}
	rows, err := s.db.Query(`SELECT id FROM wants`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		existing[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	imported := 0
	for _, want := range wants {
		if strings.TrimSpace(want.ID) == "" || existing[want.ID] {
			continue
		}
		if err := s.UpsertWant(RowFromMatchWant(want)); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

// PriceObservation is one storable hotspot price sighting.
type PriceObservation struct {
	NormKey       string
	Price         float64
	UnitPrice     *float64
	PublicationID string
	Source        string
}

// RecordObservations stores many sightings in one transaction instead of one
// implicit transaction per row.
func (s *Store) RecordObservations(retailer string, observations []PriceObservation) error {
	if len(observations) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO price_observations(norm_key, retailer, price, unit_price, observed_at, publication_id, source)
		VALUES(?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	observedAt := time.Now().UTC().Format(time.RFC3339Nano)
	for _, obs := range observations {
		var unit any
		if obs.UnitPrice != nil {
			unit = *obs.UnitPrice
		}
		if _, err := stmt.Exec(obs.NormKey, retailer, obs.Price, unit, observedAt, obs.PublicationID, obs.Source); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BaselineSince returns min/avg/count of observations at or after since.
// ok is false when nothing was ever observed.
func (s *Store) BaselineSince(retailer, normKey string, since time.Time) (min, avg float64, n int, ok bool, err error) {
	var minV, avgV sql.NullFloat64
	var count int
	err = s.db.QueryRow(`SELECT MIN(price), AVG(price), COUNT(*) FROM price_observations
		WHERE retailer = ? AND norm_key = ? AND observed_at >= ?`,
		retailer, normKey, since.UTC().Format(time.RFC3339Nano)).Scan(&minV, &avgV, &count)
	if err != nil {
		return 0, 0, 0, false, err
	}
	if count == 0 || !minV.Valid {
		return 0, 0, 0, false, nil
	}
	avg = 0
	if avgV.Valid {
		avg = avgV.Float64
	}
	return minV.Float64, avg, count, true, nil
}

// CheapestOutside returns the cheapest observation for a product from
// publications other than excludePublication. ok is false when no other
// publication ever carried the product — the current publication's price
// is then the first data point, not a price drop.
func (s *Store) CheapestOutside(retailer, normKey, excludePublication string) (min float64, n int, ok bool, err error) {
	var minV sql.NullFloat64
	var count int
	err = s.db.QueryRow(`SELECT MIN(price), COUNT(*) FROM price_observations
		WHERE retailer = ? AND norm_key = ? AND publication_id != ?`,
		retailer, normKey, excludePublication).Scan(&minV, &count)
	if err != nil {
		return 0, 0, false, err
	}
	if count == 0 || !minV.Valid {
		return 0, 0, false, nil
	}
	return minV.Float64, count, true, nil
}

// PutPrejVerdict caches a prej.dk verdict JSON for one publication + key.
func (s *Store) PutPrejVerdict(publicationID, normKey, verdictJSON string) error {
	_, err := s.db.Exec(`INSERT INTO prej_cache(publication_id, norm_key, verdict, fetched_at)
		VALUES(?, ?, ?, ?) ON CONFLICT(publication_id, norm_key) DO UPDATE SET
		verdict=excluded.verdict, fetched_at=excluded.fetched_at`,
		publicationID, normKey, verdictJSON, time.Now().UTC().Format(time.RFC3339))
	return err
}

// GetPrejVerdict returns a cached verdict for one publication + key.
func (s *Store) GetPrejVerdict(publicationID, normKey string) (string, bool, error) {
	var verdict string
	err := s.db.QueryRow(`SELECT verdict FROM prej_cache WHERE publication_id = ? AND norm_key = ?`,
		publicationID, normKey).Scan(&verdict)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return verdict, true, nil
}

// FeedState tracks one retailer's channel and last posted publication.
type FeedState struct {
	Retailer          string
	ChannelID         int64
	LastPublicationID string
}

// GetFeed returns stored feed state, zero-valued when unknown.
func (s *Store) GetFeed(retailer string) (FeedState, error) {
	state := FeedState{Retailer: retailer}
	err := s.db.QueryRow(`SELECT channel_id, last_publication_id FROM feeds WHERE retailer = ?`,
		retailer).Scan(&state.ChannelID, &state.LastPublicationID)
	if err == sql.ErrNoRows {
		return state, nil
	}
	return state, err
}

// PutFeed stores feed state.
func (s *Store) PutFeed(state FeedState) error {
	_, err := s.db.Exec(`INSERT INTO feeds(retailer, channel_id, last_publication_id)
		VALUES(?, ?, ?) ON CONFLICT(retailer) DO UPDATE SET
		channel_id=excluded.channel_id, last_publication_id=excluded.last_publication_id`,
		state.Retailer, state.ChannelID, state.LastPublicationID)
	return err
}

// AddPostedDeals records posted offer keys for a publication.
func (s *Store) AddPostedDeals(retailer, publicationID string, keys []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO posted_deals(retailer, publication_id, offer_key)
			VALUES(?, ?, ?) ON CONFLICT DO NOTHING`, retailer, publicationID, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PostedKeys returns the posted offer keys for a publication.
func (s *Store) PostedKeys(retailer, publicationID string) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT offer_key FROM posted_deals WHERE retailer = ? AND publication_id = ?`,
		retailer, publicationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, rows.Err()
}

// Stats holds row counts for /stats and health checks.
type Stats struct {
	Wants        int
	Observations int
	PrejCached   int
	Feeds        int
	Concepts     int
	Alerts       int
}

// Stats returns row counts across tables.
func (s *Store) Stats() (Stats, error) {
	var stats Stats
	for _, query := range []struct {
		sql string
		dst *int
	}{
		{`SELECT COUNT(*) FROM wants`, &stats.Wants},
		{`SELECT COUNT(*) FROM price_observations`, &stats.Observations},
		{`SELECT COUNT(*) FROM prej_cache`, &stats.PrejCached},
		{`SELECT COUNT(*) FROM feeds`, &stats.Feeds},
		{`SELECT COUNT(*) FROM concepts`, &stats.Concepts},
		{`SELECT COUNT(*) FROM price_alerts`, &stats.Alerts},
	} {
		if err := s.db.QueryRow(query.sql).Scan(query.dst); err != nil {
			return Stats{}, err
		}
	}
	return stats, nil
}

// Retention windows for the tables that are appended to on every run. The
// price history feeds the "cheapest in the last N weeks" baseline, so it keeps
// about a year; the prej.dk verdict cache only ever matters for the current
// publication and older entries are dead weight.
const (
	PriceHistoryRetention = 400 * 24 * time.Hour
	PrejCacheRetention    = 60 * 24 * time.Hour
)

// PruneStats reports how many rows a pruning run removed.
type PruneStats struct {
	Observations int64
	PrejCached   int64
	PostedDeals  int64
}

// Total reports the number of removed rows across all tables.
func (p PruneStats) Total() int64 { return p.Observations + p.PrejCached + p.PostedDeals }

// PruneHistory deletes rows older than the retention windows. Without it the
// history tables grow forever on a long-running deployment. now is passed in so
// callers (and tests) control the clock.
func (s *Store) PruneHistory(now time.Time) (PruneStats, error) {
	var stats PruneStats
	observationCutoff := now.Add(-PriceHistoryRetention).UTC().Format(time.RFC3339Nano)
	removed, err := s.db.Exec(`DELETE FROM price_observations WHERE observed_at < ?`, observationCutoff)
	if err != nil {
		return stats, fmt.Errorf("prune price observations: %w", err)
	}
	if stats.Observations, err = removed.RowsAffected(); err != nil {
		return stats, err
	}

	cacheCutoff := now.Add(-PrejCacheRetention).UTC().Format(time.RFC3339)
	removed, err = s.db.Exec(`DELETE FROM prej_cache WHERE fetched_at < ?`, cacheCutoff)
	if err != nil {
		return stats, fmt.Errorf("prune prej cache: %w", err)
	}
	if stats.PrejCached, err = removed.RowsAffected(); err != nil {
		return stats, err
	}

	// Posted offer keys are only read for the publication a feed is currently
	// on, so every other publication can go. A feed with no recorded
	// publication yet keeps its keys: without a current publication there is
	// nothing to compare against, and deleting them could break an in-flight
	// run.
	removed, err = s.db.Exec(`DELETE FROM posted_deals WHERE NOT EXISTS (
		SELECT 1 FROM feeds
		WHERE feeds.retailer = posted_deals.retailer
		  AND (feeds.last_publication_id = '' OR feeds.last_publication_id = posted_deals.publication_id))`)
	if err != nil {
		return stats, fmt.Errorf("prune posted deals: %w", err)
	}
	if stats.PostedDeals, err = removed.RowsAffected(); err != nil {
		return stats, err
	}
	return stats, nil
}

func decodeStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '[' || raw[len(raw)-1] != ']' {
		return nil
	}
	body := strings.TrimSpace(raw[1 : len(raw)-1])
	if body == "" {
		return nil
	}
	var out []string
	for len(body) > 0 {
		body = strings.TrimSpace(body)
		if len(body) == 0 || body[0] != '"' {
			return nil
		}
		end, escaped := -1, false
		for i := 1; i < len(body); i++ {
			if escaped {
				escaped = false
				continue
			}
			if body[i] == '\\' {
				escaped = true
				continue
			}
			if body[i] == '"' {
				end = i
				break
			}
		}
		if end < 0 {
			return nil
		}
		value, err := strconv.Unquote(body[:end+1])
		if err != nil {
			return nil
		}
		out = append(out, value)
		body = strings.TrimSpace(body[end+1:])
		if body == "" {
			break
		}
		if body[0] != ',' {
			return nil
		}
		body = body[1:]
	}
	return out
}

func encodeStringList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
