package service

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/ocr"
	"spar-tilbuds-bot/internal/search"
	"spar-tilbuds-bot/internal/store"

	"tjekapi"
	"tjekapi/pdf"
)

const (
	workRoot         = "work"
	workRetentionAge = 14 * 24 * time.Hour
)

type DealService struct {
	tjekAPI      *tjekapi.Client
	pdfProcessor *pdf.Processor
	logger       *slog.Logger

	// stateMu guards the two pieces of mutable wiring. Both are normally set
	// once at startup and then read from every interaction goroutine and the
	// cron loop, so unsynchronised access would be a data race.
	stateMu sync.RWMutex
	matcher match.Matcher
	store   *store.Store
}

// SetStore attaches SQLite state (wants mirror, price history, prej cache).
// A nil store disables persistence-dependent features; matching still works.
func (s *DealService) SetStore(db *store.Store) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.store = db
}

// storeDB returns the attached store, or nil when none is configured.
func (s *DealService) storeDB() *store.Store {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.store
}

// SetMatcher selects the matching strategy. A nil matcher keeps the current
// one; services default to the query matcher.
func (s *DealService) SetMatcher(matcher match.Matcher) {
	if matcher == nil {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.matcher = matcher
}

func (s *DealService) activeMatcher() match.Matcher {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.matcher == nil {
		return match.ForName(match.DefaultMatcher)
	}
	return s.matcher
}

// matcherFor returns the active matcher bound to one feed's retailer when the
// matcher supports store-scoped queries. Without the binding, a query filter
// such as store:føtex can never match, because the adapter has no store name
// to canonicalize.
func (s *DealService) matcherFor(retailer string) match.Matcher {
	matcher := s.activeMatcher()
	if bound, ok := matcher.(match.RetailerMatcher); ok {
		return bound.ForRetailer(retailer)
	}
	return matcher
}

func NewDealService(tjekAPIClient *tjekapi.Client, pdfProcessorClient *pdf.Processor, logger *slog.Logger) *DealService {
	if logger == nil {
		logger = slog.Default()
	}
	if tjekAPIClient == nil {
		tjekAPIClient = tjekapi.NewClient(tjekapi.WithLogger(logger))
	}
	if pdfProcessorClient == nil {
		processorOptions := []pdf.Option{pdf.WithLogger(logger)}
		if paddleOCROption, enabled := ocr.NewPaddleOCROption(logger); enabled {
			processorOptions = append(processorOptions, paddleOCROption)
		}
		pdfProcessorClient = pdf.NewProcessor(processorOptions...)
	}

	return &DealService{tjekAPI: tjekAPIClient, pdfProcessor: pdfProcessorClient, logger: logger}
}

func (s *DealService) GetCurrentWeek() (string, error) {
	return s.GetCurrentWeekContext(context.Background())
}

// Close releases resources owned by the external tjekapi PDF processor.
func (s *DealService) Close() error {
	if s == nil || s.pdfProcessor == nil {
		return nil
	}
	return s.pdfProcessor.Close()
}

func (s *DealService) GetCurrentWeekContext(ctx context.Context) (string, error) {
	return s.GetRetailerWeekContext(ctx, RetailerSPAR)
}

// GetRetailerWeekContext resolves the current publication for a feed.
func (s *DealService) GetRetailerWeekContext(ctx context.Context, retailer string) (string, error) {
	business, err := tjekBusinessName(retailer)
	if err != nil {
		return "", err
	}
	return s.tjekAPI.CurrentPublicationID(ctx, business)
}

func (s *DealService) FetchAndProcessDeals(keywords []string) ([]tjekapi.Deal, error) {
	return s.FetchAndProcessDealsContext(context.Background(), keywords)
}

// FetchAndProcessDealsContext is the legacy keyword entrypoint. It converts
// keywords to single-term wants and matches with the legacy keyword matcher,
// preserving old behavior while the callers migrate to wants.
func (s *DealService) FetchAndProcessDealsContext(ctx context.Context, keywords []string) ([]tjekapi.Deal, error) {
	scored, err := s.fetchAndProcess(ctx, RetailerSPAR, match.WantsFromKeywords(keywords), match.KeywordMatcher{})
	if err != nil {
		return nil, err
	}
	deals := make([]tjekapi.Deal, 0, len(scored))
	for _, sd := range scored {
		deals = append(deals, sd.Deal)
	}
	return deals, nil
}

// FetchAndProcessDealsForWants fetches the current SPAR publication, matches
// hotspots with the service matcher, and returns ranked deals.
func (s *DealService) FetchAndProcessDealsForWants(ctx context.Context, wants []match.Want) ([]match.ScoredDeal, error) {
	return s.FetchAndProcessRetailerDeals(ctx, RetailerSPAR, wants)
}

// FetchAndProcessRetailerDeals is the per-feed pipeline: match, deduplicate,
// enrich with images, price-check, rank and cap.
func (s *DealService) FetchAndProcessRetailerDeals(ctx context.Context, retailer string, wants []match.Want) ([]match.ScoredDeal, error) {
	return s.fetchAndProcess(ctx, retailer, match.FilterWantsByRetailer(wants, retailer), s.matcherFor(retailer))
}

// TopDeals returns the top n ranked deals for a retailer without downloading
// the catalog or cropping images. Prices come from hotspot names.
func (s *DealService) TopDeals(ctx context.Context, retailer string, wants []match.Want, n int) ([]match.ScoredDeal, error) {
	publicationID, err := s.GetRetailerWeekContext(ctx, retailer)
	if err != nil {
		return nil, err
	}
	hotspots, err := s.tjekAPI.FetchAllHotspots(ctx, publicationID)
	if err != nil {
		return nil, err
	}
	filtered := match.FilterWantsByRetailer(wants, retailer)
	scored := s.matcherFor(retailer).Match(ctx, hotspots, filtered)
	final := tjekapi.GroupAndDeduplicateOffers(toDeals(scored), s.logger)
	prior := make(map[string]match.ScoredDeal, len(scored))
	for _, sd := range scored {
		prior[dealKey(sd.Deal)] = sd
	}
	resolved := resolveDeals(filtered, prior, final)
	s.recordObservations(retailer, publicationID, resolved)
	return rankDeals(s.enrichPrej(ctx, retailer, publicationID, resolved), n), nil
}

func toDeals(scored []match.ScoredDeal) []tjekapi.Deal {
	deals := make([]tjekapi.Deal, 0, len(scored))
	for _, sd := range scored {
		deals = append(deals, sd.Deal)
	}
	return deals
}

// SearchDeals matches a free-text query against one retailer's current
// publication without downloading the catalog, cropping images, or posting
// anything. The query uses the full query language: terms, exclusions,
// phrases, price/unit-price/discount/store filters (see the query
// documentation). Malformed queries fail with a typed *search.ParseError
// whose UserMessage is suitable for Discord replies. Results rank by
// descending score (product-name hits before description-only hits).
func (s *DealService) SearchDeals(ctx context.Context, retailer, query string, limit int) ([]match.ScoredDeal, error) {
	publicationID, err := s.GetRetailerWeekContext(ctx, retailer)
	if err != nil {
		return nil, err
	}
	hotspots, err := s.tjekAPI.FetchAllHotspots(ctx, publicationID)
	if err != nil {
		return nil, err
	}
	// Compile once and fail fast: invalid queries are rejected before any
	// offer is evaluated.
	want := match.Want{ID: "søgning", Describe: query, Query: query}
	if _, err := match.WantQuery(want, match.DefaultConcepts()); err != nil {
		return nil, err
	}
	scored := match.QueryMatcher{
		Concepts: match.DefaultConcepts(),
		Config:   search.DefaultConfig(),
		Retailer: retailer,
	}.Match(ctx, hotspots, []match.Want{want})
	match.SortByScoreDesc(scored)
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

func (s *DealService) fetchAndProcess(ctx context.Context, retailer string, wants []match.Want, matcher match.Matcher) ([]match.ScoredDeal, error) {
	s.logger.Info("starting to fetch and process deals",
		slog.String("matcher", matcher.Name()), slog.String("retailer", retailer))

	publicationID, err := s.GetRetailerWeekContext(ctx, retailer)
	if err != nil {
		return nil, err
	}
	s.logger.Info("publication ID", slog.String("publication_id", publicationID))

	// The publication ID comes from an upstream API; keep it from escaping the
	// work directory through characters it should never contain.
	workDir := filepath.Join(workRoot, safePublicationID(publicationID))
	if err := cleanupOldWorkDirs(workRoot, workRetentionAge, workDir, s.logger); err != nil {
		s.logger.Warn("failed to cleanup old work folders", slog.String("error", err.Error()))
	}

	hotspots, err := s.tjekAPI.FetchAllHotspots(ctx, publicationID)
	if err != nil {
		return nil, err
	}
	s.logger.Info("fetched hotspots", slog.Int("count", len(hotspots)))

	scored := matcher.Match(ctx, hotspots, wants)
	prior := make(map[string]match.ScoredDeal, len(scored))
	deals := make([]tjekapi.Deal, 0, len(scored))
	for _, sd := range scored {
		prior[dealKey(sd.Deal)] = sd
		deals = append(deals, sd.Deal)
	}
	finalDeals := tjekapi.GroupAndDeduplicateOffers(deals, s.logger)

	outputDir := filepath.Join(workDir, "cropped")
	var dealsWithImages []tjekapi.Deal
	pdfPath := filepath.Join(workDir, "catalog.pdf")
	if _, err := os.Stat(pdfPath); err == nil {
		// Reuse a previously downloaded PDF.
		dealsWithImages = s.pdfProcessor.ProcessDealImages(pdfPath, finalDeals, outputDir)
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		pdfURL, err := s.tjekAPI.PDFURLFromPublication(ctx, publicationID)
		if err != nil {
			if tjekapi.IsPDFNotAvailable(err) {
				// Paged-only catalog (e.g. føtex): no PDF, crop from per-page images.
				return s.finishPagedDeals(ctx, retailer, publicationID, wants, prior, workDir, finalDeals, outputDir)
			}
			return nil, err
		}
		if err := s.pdfProcessor.DownloadPDF(ctx, pdfURL, pdfPath); err != nil {
			return nil, err
		}
		dealsWithImages = s.pdfProcessor.ProcessDealImages(pdfPath, finalDeals, outputDir)
	}

	validDeals := make([]tjekapi.Deal, 0, len(dealsWithImages))
	for _, deal := range dealsWithImages {
		if deal.ImagePath == "" || deal.Price == nil || *deal.Price <= 0 {
			continue
		}
		validDeals = append(validDeals, deal)
	}
	ranked := s.finishDeals(ctx, retailer, publicationID, wants, prior, validDeals)

	s.logger.Info("valid deals with images and price", slog.Int("count", len(ranked)))
	return ranked, nil
}

// safePublicationID reduces an upstream publication ID to a single safe path
// element. Real IDs are short alphanumeric strings such as "ujLGsQ0J"; every
// other character (including '.' and separators) is replaced so a malformed or
// hostile upstream response cannot address paths outside work/.
func safePublicationID(publicationID string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, publicationID)
	if cleaned == "" {
		return "unknown"
	}
	return cleaned
}

// finishPagedDeals handles paged-only catalogs (e.g. føtex) that have no
// downloadable PDF: it downloads per-page images and crops deal images from
// those instead of a PDF.
func (s *DealService) finishPagedDeals(ctx context.Context, retailer, publicationID string, wants []match.Want, prior map[string]match.ScoredDeal, workDir string, finalDeals []tjekapi.Deal, outputDir string) ([]match.ScoredDeal, error) {
	s.logger.Info("catalog has no PDF, falling back to paged images", slog.String("publication_id", publicationID))
	pageURLs, err := s.tjekAPI.CatalogPageImageURLs(ctx, publicationID)
	if err != nil {
		return nil, err
	}
	pagesDir := filepath.Join(workDir, "pages")
	pagePaths, err := s.pdfProcessor.DownloadCatalogPages(ctx, pageURLs, pagesDir)
	if err != nil {
		return nil, err
	}
	dealsWithImages := s.pdfProcessor.ProcessDealImagesFromPages(pagePaths, finalDeals, outputDir)
	validDeals := make([]tjekapi.Deal, 0, len(dealsWithImages))
	for _, deal := range dealsWithImages {
		if deal.ImagePath == "" || deal.Price == nil || *deal.Price <= 0 {
			continue
		}
		validDeals = append(validDeals, deal)
	}
	ranked := s.finishDeals(ctx, retailer, publicationID, wants, prior, validDeals)
	s.logger.Info("valid paged deals with images and price", slog.Int("count", len(ranked)))
	return ranked, nil
}
