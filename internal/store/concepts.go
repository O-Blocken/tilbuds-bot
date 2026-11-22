package store

import (
	"fmt"
	"strings"
	"time"
)

// This file persists admin-managed search concepts and price alerts in
// bot.db (next to the TOML config, so both survive container updates in
// the Docker deployment).

// ConceptRow is one persisted concept override. Rows layer over the
// concepts.toml base: a row with the same name replaces the base concept,
// a new name adds one, and Deleted hides a base concept. Aliases and Refs
// are JSON string arrays.
type ConceptRow struct {
	Name        string
	Description string
	Aliases     []string
	Refs        []string
	Deleted     bool
	UpdatedAt   string
}

// ListConcepts returns all persisted concept rows ordered by name.
func (s *Store) ListConcepts() ([]ConceptRow, error) {
	rows, err := s.db.Query(`SELECT name, description, aliases, refs, deleted, updated_at FROM concepts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConceptRow
	for rows.Next() {
		var row ConceptRow
		var aliases, refs string
		var deleted int
		if err := rows.Scan(&row.Name, &row.Description, &aliases, &refs, &deleted, &row.UpdatedAt); err != nil {
			return nil, err
		}
		row.Aliases = decodeStringList(aliases)
		row.Refs = decodeStringList(refs)
		row.Deleted = deleted != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

// UpsertConcept stores one concept override. Deleted rows are tombstones
// hiding a concepts.toml entry; the name alone is required for them.
func (s *Store) UpsertConcept(row ConceptRow) error {
	name := strings.TrimSpace(row.Name)
	if name == "" {
		return fmt.Errorf("concept name is empty")
	}
	aliases := encodeStringList(nonNilStrings(row.Aliases))
	refs := encodeStringList(nonNilStrings(row.Refs))
	deleted := 0
	if row.Deleted {
		deleted = 1
	}
	_, err := s.db.Exec(`INSERT INTO concepts(name, description, aliases, refs, deleted, updated_at)
		VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT(name) DO UPDATE SET
		description=excluded.description, aliases=excluded.aliases, refs=excluded.refs,
		deleted=excluded.deleted, updated_at=excluded.updated_at`,
		name, strings.TrimSpace(row.Description), aliases, refs, deleted,
		time.Now().UTC().Format(time.RFC3339))
	return err
}

// DeleteConcept removes a concept override entirely, restoring the
// concepts.toml base entry when one exists. It reports whether a row was
// removed.
func (s *Store) DeleteConcept(name string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM concepts WHERE name = ?`, strings.TrimSpace(name))
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

// PriceAlertRow is one persisted price alert. TargetPrice nil means an
// all-time-low alert: it fires when the matched offer undercuts the
// cheapest price seen for the same product. FiredPublication records the
// publication the alert last fired on so a weekly feed pings once.
type PriceAlertRow struct {
	ID               int64
	WantID           string
	Query            string
	TargetPrice      *float64
	Retailer         string
	ChannelID        int64
	UserID           string
	Active           bool
	FiredPublication string
	CreatedAt        string
}

// ListPriceAlerts returns all alerts ordered by id.
func (s *Store) ListPriceAlerts() ([]PriceAlertRow, error) {
	rows, err := s.db.Query(`SELECT id, want_id, query, target_price, retailer, channel_id, user_id, active, fired_publication, created_at
		FROM price_alerts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PriceAlertRow
	for rows.Next() {
		var row PriceAlertRow
		var target sqlNullFloat64
		var active int
		if err := rows.Scan(&row.ID, &row.WantID, &row.Query, &target, &row.Retailer,
			&row.ChannelID, &row.UserID, &active, &row.FiredPublication, &row.CreatedAt); err != nil {
			return nil, err
		}
		row.TargetPrice = target.Value
		row.Active = active != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

// AddPriceAlert stores a new alert and returns its id.
func (s *Store) AddPriceAlert(row PriceAlertRow) (int64, error) {
	query := strings.TrimSpace(row.Query)
	if query == "" {
		return 0, fmt.Errorf("alert query is empty")
	}
	var target any
	if row.TargetPrice != nil {
		target = *row.TargetPrice
	}
	// New alerts always start active.
	res, err := s.db.Exec(`INSERT INTO price_alerts(want_id, query, target_price, retailer, channel_id, user_id, active, fired_publication, created_at)
		VALUES(?, ?, ?, ?, ?, ?, 1, '', ?)`,
		strings.TrimSpace(row.WantID), query, target, strings.TrimSpace(row.Retailer),
		row.ChannelID, strings.TrimSpace(row.UserID), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeletePriceAlert removes an alert. It reports whether one existed.
func (s *Store) DeletePriceAlert(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM price_alerts WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

// MarkPriceAlertFired records the publication an alert fired on so the
// same weekly feed never pings twice.
func (s *Store) MarkPriceAlertFired(id int64, publicationID string) error {
	_, err := s.db.Exec(`UPDATE price_alerts SET fired_publication = ? WHERE id = ?`,
		strings.TrimSpace(publicationID), id)
	return err
}

// sqlNullFloat64 is a tiny local wrapper so the store keeps its single
// database/sql dependency at the schema boundary only.
type sqlNullFloat64 struct {
	Value *float64
}

// Scan implements the sql.Scanner interface.
func (n *sqlNullFloat64) Scan(src any) error {
	if src == nil {
		n.Value = nil
		return nil
	}
	switch v := src.(type) {
	case float64:
		n.Value = &v
		return nil
	case int64:
		f := float64(v)
		n.Value = &f
		return nil
	}
	return fmt.Errorf("cannot scan %T into price", src)
}
