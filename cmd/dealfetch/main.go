// Command dealfetch downloads the current SPAR, Føtex and Lidl publications
// from Tjek and writes them as TOML fixtures for the search engine's testdata.
//
// Usage (from the repository root):
//
//	go run ./cmd/dealfetch -out internal/search/testdata/real_offers.toml
//
// The output is used to build and verify the concept/alias registry against
// real offer wording and to run the query engine over real deal data.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tjekapi"
)

// detailBatchSize is the number of offers per app-cache request. The
// etilbudsavis app cache rejects oversized key batches with HTTP 400, and
// each offer needs two keys (offer + offerProducts), so keep chunks small.
const detailBatchSize = 6

// detailWorkers is the number of concurrent detail batches.
const detailWorkers = 4

type fixtureOffer struct {
	Retailer      string
	OfferID       string
	OfferName     string
	CleanName     string
	Price         *float64
	IsAppPrice    bool
	UnitPrice     *float64
	BaseUnit      string
	UnitSymbol    string
	UnitSizeFrom  *float64
	UnitSizeTo    *float64
	PieceCountGet *int
	PieceCountFor *int
	NormalPrice   *float64
	Savings       *float64
	RelSavings    *float64
	AppPrice      *float64
	MemberPrice   *float64
	FromPrice     *float64
	Description   string
	Department    string
	ValidFrom     string
	ValidUntil    string
	PageNumber    int
}

func main() {
	out := flag.String("out", "internal/search/testdata/real_offers.toml", "output TOML path")
	withDetails := flag.Bool("details", true, "fetch full offer details (unit prices, departments)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := tjekapi.NewClient(tjekapi.WithLogger(logger))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	retailers := map[string]string{"spar": "SPAR", "fotex": "Føtex", "lidl": "Lidl"}
	names := make([]string, 0, len(retailers))
	for name := range retailers {
		names = append(names, name)
	}
	sort.Strings(names)

	var offers []fixtureOffer
	for _, retailer := range names {
		publicationID, err := client.CurrentPublicationID(ctx, retailers[retailer])
		if err != nil {
			logger.Error("publication lookup failed", "retailer", retailer, "err", err)
			os.Exit(1)
		}
		hotspots, err := client.FetchAllHotspots(ctx, publicationID)
		if err != nil {
			logger.Error("hotspot fetch failed", "retailer", retailer, "err", err)
			os.Exit(1)
		}
		logger.Info("hotspots", "retailer", retailer, "count", len(hotspots))

		details := map[string]tjekapi.OfferDetails{}
		if *withDetails {
			ids := make([]string, 0, len(hotspots))
			seen := map[string]bool{}
			for _, h := range hotspots {
				if h.OfferID != "" && !seen[h.OfferID] {
					seen[h.OfferID] = true
					ids = append(ids, h.OfferID)
				}
			}
			details = fetchDetails(ctx, client, ids, logger)
			logger.Info("details", "retailer", retailer, "count", len(details))
		}

		for _, h := range hotspots {
			parsed := tjekapi.ParseOfferName(h.OfferName)
			offer := fixtureOffer{
				Retailer:   retailer,
				OfferID:    h.OfferID,
				OfferName:  h.OfferName,
				CleanName:  parsed.CleanName,
				Price:      parsed.Price,
				IsAppPrice: parsed.IsAppPrice,
				PageNumber: h.PageNumber,
			}
			if d, ok := details[h.OfferID]; ok {
				offer.UnitPrice = d.UnitPrice
				offer.BaseUnit = d.BaseUnit
				offer.UnitSymbol = d.UnitSymbol
				offer.UnitSizeFrom = d.UnitSizeFrom
				offer.UnitSizeTo = d.UnitSizeTo
				offer.PieceCountGet = d.PieceCountGet
				offer.PieceCountFor = d.PieceCountGetFor
				offer.Savings = d.Savings
				offer.RelSavings = d.RelativeSavings
				offer.AppPrice = d.AppPrice
				offer.MemberPrice = d.MembershipPrice
				offer.FromPrice = d.FromPrice
				offer.Description = d.Description
				offer.Department = d.DepartmentSlug
				offer.ValidFrom = d.ValidFrom
				offer.ValidUntil = d.ValidUntil
				if d.Savings != nil && d.Price != nil {
					normal := *d.Price + *d.Savings
					offer.NormalPrice = &normal
				}
				if offer.Price == nil {
					offer.Price = d.Price
				}
			}
			offers = append(offers, offer)
		}
	}

	sort.Slice(offers, func(i, j int) bool {
		if offers[i].Retailer != offers[j].Retailer {
			return offers[i].Retailer < offers[j].Retailer
		}
		return offers[i].OfferName < offers[j].OfferName
	})

	var payload strings.Builder
	fmt.Fprintf(&payload, "fetched_at = %s\n\n", strconv.Quote(time.Now().UTC().Format(time.RFC3339)))
	writeString := func(key, value string) { fmt.Fprintf(&payload, "%s = %s\n", key, strconv.Quote(value)) }
	writeNumber := func(key string, value *float64) {
		if value != nil {
			fmt.Fprintf(&payload, "%s = %s\n", key, strconv.FormatFloat(*value, 'f', -1, 64))
		}
	}
	for _, offer := range offers {
		payload.WriteString("[[offers]]\n")
		writeString("retailer", offer.Retailer)
		writeString("offer_name", offer.OfferName)
		writeString("clean_name", offer.CleanName)
		writeNumber("price", offer.Price)
		fmt.Fprintf(&payload, "is_app_price = %t\n", offer.IsAppPrice)
		writeNumber("unit_price", offer.UnitPrice)
		writeString("base_unit", offer.BaseUnit)
		writeNumber("normal_price", offer.NormalPrice)
		writeNumber("rel_savings", offer.RelSavings)
		writeString("description", offer.Description)
		writeString("department", offer.Department)
		payload.WriteByte('\n')
	}
	if err := os.WriteFile(*out, []byte(payload.String()), 0o644); err != nil {
		logger.Error("write failed", "err", err)
		os.Exit(1)
	}
	logger.Info("wrote fixture", "path", *out, "offers", len(offers))
}

// fetchDetails downloads offer details in small concurrent batches and
// retries each failed batch before dropping it.
func fetchDetails(ctx context.Context, client *tjekapi.Client, ids []string, logger *slog.Logger) map[string]tjekapi.OfferDetails {
	// Buffered so the producer never blocks on a worker set that has already
	// exited on cancellation, and sized for every batch so no send can stall.
	batchCount := (len(ids) + detailBatchSize - 1) / detailBatchSize
	batches := make(chan []string, max(batchCount, 1))
	var mu sync.Mutex
	var wg sync.WaitGroup
	details := make(map[string]tjekapi.OfferDetails, len(ids))
	for range detailWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range batches {
				got := fetchDetailBatchWithRetry(ctx, client, batch, logger)
				mu.Lock()
				maps.Copy(details, got)
				mu.Unlock()
			}
		}()
	}
	// A labelled break, because `break` inside select would only leave the
	// select and keep sending batches to workers that already gave up.
sendBatches:
	for start := 0; start < len(ids); start += detailBatchSize {
		select {
		case <-ctx.Done():
			break sendBatches
		case batches <- ids[start:min(start+detailBatchSize, len(ids))]:
		}
	}
	close(batches)
	wg.Wait()
	return details
}

// fetchDetailBatchWithRetry downloads one batch, retrying transient failures
// three times. A cancelled context stops the retries immediately.
func fetchDetailBatchWithRetry(ctx context.Context, client *tjekapi.Client, batch []string, logger *slog.Logger) map[string]tjekapi.OfferDetails {
	var got map[string]tjekapi.OfferDetails
	for attempt := 1; attempt <= 3; attempt++ {
		var err error
		got, err = client.FetchOfferDetailsBatch(ctx, batch)
		if err == nil {
			return got
		}
		logger.Warn("detail batch failed, retrying", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
		}
	}
	return nil
}
