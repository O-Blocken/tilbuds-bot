package store

import (
	"path/filepath"
	"testing"
	"time"

	"spar-tilbuds-bot/internal/match"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func floatPtr(f float64) *float64 { return &f }

func boolPtr(b bool) *bool { return &b }

func TestWantsCRUDRoundTrip(t *testing.T) {
	store := openTestStore(t)
	empty, err := store.WantsEmpty()
	if err != nil || !empty {
		t.Fatalf("fresh store empty=%v err=%v", empty, err)
	}
	want := match.Want{
		ID: "sodavand", Describe: "d", Include: []string{"cola", "pepsi"},
		Exclude: []string{"lakrids"}, MaxPrice: floatPtr(25), AllowAppPrice: boolPtr(false),
		Threshold: 0.5, Retailers: []string{"spar"},
	}
	if err := store.UpsertWant(RowFromMatchWant(want)); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListWants()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("wants = %d, want 1", len(rows))
	}
	got := rows[0].ToMatchWant()
	if got.ID != "sodavand" || got.Describe != "d" || len(got.Include) != 2 || len(got.Exclude) != 1 {
		t.Errorf("round trip = %+v", got)
	}
	if got.MaxPrice == nil || *got.MaxPrice != 25 || got.AllowAppPrice == nil || *got.AllowAppPrice {
		t.Errorf("pointers lost: %+v", got)
	}
	if got.Threshold != 0.5 || len(got.Retailers) != 1 {
		t.Errorf("threshold/retailers lost: %+v", got)
	}
	deleted, err := store.DeleteWant("sodavand")
	if err != nil || !deleted {
		t.Fatalf("delete = %v %v", deleted, err)
	}
	deleted, err = store.DeleteWant("sodavand")
	if err != nil || deleted {
		t.Fatalf("second delete = %v %v", deleted, err)
	}
}

func TestImportMatchWantsKeepsExisting(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertWant(WantRow{ID: "keep", Describe: "managed", Includes: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	imported, err := store.ImportMatchWants([]match.Want{
		{ID: "keep", Describe: "toml", Include: []string{"b"}},
		{ID: "new", Describe: "toml", Include: []string{"c"}},
	})
	if err != nil || imported != 1 {
		t.Fatalf("import = %d %v, want 1", imported, err)
	}
	rows, err := store.ListWants()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]WantRow{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	if byID["keep"].Describe != "managed" {
		t.Errorf("existing want overwritten: %+v", byID["keep"])
	}
	if byID["new"].Describe != "toml" {
		t.Errorf("new want missing: %+v", byID)
	}
}

func TestPriceBaseline(t *testing.T) {
	store := openTestStore(t)
	now := time.Now()
	for _, price := range []float64{20, 18, 22} {
		if err := store.RecordObservations("spar", []PriceObservation{
			{NormKey: "kims chips", Price: price, PublicationID: "pub", Source: "hotspot"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = now
	min, avg, n, ok, err := store.BaselineSince("spar", "kims chips", time.Now().Add(-8*7*24*time.Hour))
	if err != nil || !ok || n != 3 {
		t.Fatalf("baseline = %v %v %d %v %v", min, avg, n, ok, err)
	}
	if min != 18 || avg != 20 {
		t.Errorf("baseline min/avg = %v/%v, want 18/20", min, avg)
	}
	_, _, _, ok, err = store.BaselineSince("fotex", "kims chips", time.Now().Add(-time.Hour))
	if err != nil || ok {
		t.Errorf("other retailer ok=%v err=%v, want no data", ok, err)
	}
	// Old observations fall out of the window.
	_, _, _, ok, err = store.BaselineSince("spar", "kims chips", time.Now().Add(time.Hour))
	if err != nil || ok {
		t.Errorf("future window ok=%v err=%v, want no data", ok, err)
	}
}

func TestPrejCache(t *testing.T) {
	store := openTestStore(t)
	if _, ok, err := store.GetPrejVerdict("pub", "key"); err != nil || ok {
		t.Fatalf("empty cache = %v %v", ok, err)
	}
	if err := store.PutPrejVerdict("pub", "key", `{"good":true}`); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.GetPrejVerdict("pub", "key")
	if err != nil || !ok || got != `{"good":true}` {
		t.Fatalf("cache = %q %v %v", got, ok, err)
	}
}

func TestFeedsAndPostedKeys(t *testing.T) {
	store := openTestStore(t)
	feed, err := store.GetFeed("spar")
	if err != nil || feed.LastPublicationID != "" {
		t.Fatalf("fresh feed = %+v %v", feed, err)
	}
	if err := store.PutFeed(FeedState{Retailer: "spar", ChannelID: 7, LastPublicationID: "pub1"}); err != nil {
		t.Fatal(err)
	}
	feed, err = store.GetFeed("spar")
	if err != nil || feed.ChannelID != 7 || feed.LastPublicationID != "pub1" {
		t.Fatalf("feed = %+v %v", feed, err)
	}
	if err := store.AddPostedDeals("spar", "pub1", []string{"a", "b", ""}); err != nil {
		t.Fatal(err)
	}
	keys, err := store.PostedKeys("spar", "pub1")
	if err != nil || len(keys) != 2 || !keys["a"] || !keys["b"] {
		t.Fatalf("keys = %v %v", keys, err)
	}
	keys, err = store.PostedKeys("fotex", "pub1")
	if err != nil || len(keys) != 0 {
		t.Fatalf("other feed keys = %v %v", keys, err)
	}
}
func TestRecordObservationsBatch(t *testing.T) {
	store := openTestStore(t)
	observations := []PriceObservation{
		{NormKey: "kims chips", Price: 18, PublicationID: "pub1", Source: "hotspot"},
		{NormKey: "kims chips", Price: 20, PublicationID: "pub1", Source: "hotspot"},
		{NormKey: "pepsi max", Price: 59, PublicationID: "pub1", Source: "hotspot"},
	}
	if err := store.RecordObservations("spar", observations); err != nil {
		t.Fatal(err)
	}
	_, _, n, ok, err := store.BaselineSince("spar", "kims chips", time.Now().Add(-time.Hour))
	if err != nil || !ok || n != 2 {
		t.Errorf("baseline = n=%d ok=%v err=%v, want 2 rows", n, ok, err)
	}
	if err := store.RecordObservations("spar", nil); err != nil {
		t.Errorf("empty batch must be a no-op: %v", err)
	}
}
