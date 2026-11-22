package service_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"tjekapi"
)

func TestExternalTjekAPIHelpersBuildMatchingDeals(t *testing.T) {
	parsed := tjekapi.ParseOfferName("Kims Chips, DKK 18, App-pris")
	if parsed.CleanName != "Kims Chips" {
		t.Fatalf("CleanName = %q, want %q", parsed.CleanName, "Kims Chips")
	}
	if parsed.Price == nil || *parsed.Price != 18 {
		t.Fatalf("Price = %v, want 18", parsed.Price)
	}
	if !parsed.IsAppPrice {
		t.Fatal("IsAppPrice = false, want true")
	}

	hotspots := []tjekapi.Hotspot{
		{OfferName: "Kims Chips, DKK 18", OfferID: "chips"},
		{OfferName: "Rugbrød, DKK 12", OfferID: "bread"},
	}
	deals := tjekapi.FilterHotspotsByKeywords(
		hotspots,
		[]string{"chips"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if len(deals) != 1 || deals[0].OfferID != "chips" {
		t.Fatalf("filtered deals = %+v, want one chips deal", deals)
	}
}

func TestExternalTjekAPIFetchAllHotspotsContinuesAfterEmptyPage(t *testing.T) {
	var requestedPages pageRecorder
	client := tjekapi.NewClient(
		tjekapi.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			page := pageFromRequest(t, request)
			requestedPages.add(page)
			switch page {
			case 1:
				return jsonResponse(`{"hotspots":[{"offer":{"name":"First offer","id":"first"}}]}`), nil
			case 3:
				return jsonResponse(`{"hotspots":[{"offer":{"name":"Later offer","id":"later"}}]}`), nil
			default:
				return jsonResponse(`{"hotspots":[]}`), nil
			}
		})}),
		tjekapi.WithRateLimits(tjekapi.RateLimitConfig{}),
		tjekapi.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	hotspots, err := client.FetchAllHotspots(context.Background(), "publication")
	if err != nil {
		t.Fatalf("FetchAllHotspots() error = %v", err)
	}
	if len(hotspots) != 2 {
		t.Fatalf("len(hotspots) = %d, want 2", len(hotspots))
	}
	if hotspots[0].OfferID != "first" || hotspots[0].PageNumber != 0 {
		t.Fatalf("first hotspot = %+v, want first offer on page 0", hotspots[0])
	}
	if hotspots[1].OfferID != "later" || hotspots[1].PageNumber != 2 {
		t.Fatalf("second hotspot = %+v, want later offer on page 2", hotspots[1])
	}
	if pages := requestedPages.snapshot(); !containsInt(pages, 3) {
		t.Fatalf("requested pages = %v, want page 3 after empty page 2", pages)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func pageFromRequest(t *testing.T, request *http.Request) int {
	t.Helper()

	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	page, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		t.Fatalf("parse page from %q: %v", request.URL.Path, err)
	}
	return page
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type pageRecorder struct {
	mu    sync.Mutex
	pages []int
}

func (r *pageRecorder) add(page int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pages = append(r.pages, page)
}

func (r *pageRecorder) snapshot() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.pages...)
}
