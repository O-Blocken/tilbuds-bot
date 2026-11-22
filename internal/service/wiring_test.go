package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"spar-tilbuds-bot/internal/match"

	"tjekapi"
)

func colaHotspot() tjekapi.Hotspot {
	return tjekapi.Hotspot{OfferID: "offer-1", OfferName: "Coca Cola 1,5 l", PageNumber: 1}
}

// TestSafePublicationID guards the work-directory path element against a
// hostile or malformed upstream publication ID.
func TestSafePublicationID(t *testing.T) {
	cases := map[string]string{
		"ujLGsQ0J":         "ujLGsQ0J",
		"week-42_v2":       "week-42_v2",
		"../../etc":        "______etc",
		"..":               "__",
		"a/b":              "a_b",
		`a\b`:              "a_b",
		"":                 "unknown",
		"with space":       "with_space",
		"trailing/slash/":  "trailing_slash_",
		"null\x00byte":     "null_byte",
		"..%2f..%2fpasswd": "___2f___2fpasswd",
		"unicode-ø-æ":      "unicode-_-_",
	}
	for input, want := range cases {
		if got := safePublicationID(input); got != want {
			t.Errorf("safePublicationID(%q) = %q, want %q", input, got, want)
		}
		if strings.ContainsAny(safePublicationID(input), `/\`) {
			t.Errorf("safePublicationID(%q) still contains a path separator", input)
		}
	}
}

// TestMatcherForBindsRetailer covers the store: filter regression: an unbound
// matcher cannot canonicalize an offer's store, so store:spar never matches.
func TestMatcherForBindsRetailer(t *testing.T) {
	service := NewDealService(nil, nil, nil)
	wants := []match.Want{{ID: "cola", Query: "cola store:spar"}}
	hotspots := []tjekapi.Hotspot{colaHotspot()}

	unbound := service.activeMatcher().Match(context.Background(), hotspots, wants)
	if len(unbound) != 0 {
		t.Fatalf("unbound matcher matched %d hotspots, want 0 (store: cannot resolve)", len(unbound))
	}

	bound := service.matcherFor(RetailerFotex).Match(context.Background(), hotspots, wants)
	if len(bound) != 0 {
		t.Fatalf("fotex-bound matcher matched %d hotspots, want 0 (store is spar)", len(bound))
	}

	spar := service.matcherFor(RetailerSPAR).Match(context.Background(), hotspots, wants)
	if len(spar) != 1 {
		t.Fatalf("spar-bound matcher matched %d hotspots, want 1", len(spar))
	}
}

// TestSetMatcherIsRaceFree is meaningful under -race: SetMatcher and matcher
// reads happen from interaction goroutines and the cron loop at the same time.
func TestSetMatcherIsRaceFree(t *testing.T) {
	service := NewDealService(nil, nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				service.SetMatcher(match.KeywordMatcher{})
				return
			}
			service.SetMatcher(match.ForName(match.MatcherQuery))
		}(i)
		go func() {
			defer wg.Done()
			if got := service.activeMatcher(); got == nil {
				t.Error("activeMatcher() returned nil")
			}
			_ = service.matcherFor(RetailerSPAR)
		}()
	}
	wg.Wait()
}

// TestSetStoreIsRaceFree pairs with the matcher test: SetStore is startup-time
// wiring, but readers run concurrently and must never observe a torn value.
func TestSetStoreIsRaceFree(t *testing.T) {
	service := NewDealService(nil, nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = service.storeDB()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		service.SetStore(nil)
	}()
	wg.Wait()
}
