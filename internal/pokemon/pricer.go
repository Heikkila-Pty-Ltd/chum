package pokemon

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
)

// PriceSource is the interface that each pricing data source must implement.
type PriceSource interface {
	// Name returns the source identifier (e.g. "ebay", "tcgplayer").
	Name() string
	// FetchPrices fetches price estimates for the given card.
	FetchPrices(ctx context.Context, card CardIdentification) ([]PriceEstimate, error)
}

// Pricer orchestrates concurrent price fetching from multiple sources and
// calculates a suggested price.
type Pricer struct {
	sources   []PriceSource
	converter *CurrencyConverter
	logger    *slog.Logger
}

// NewPricer creates a Pricer that only instantiates sources listed in
// cfg.Sources. If Sources is nil or empty, no sources are created.
// Call Close() when done to release background resources.
func NewPricer(cfg PricingConfig, logger *slog.Logger) *Pricer {
	if logger == nil {
		logger = slog.Default()
	}

	converter := NewCurrencyConverter(logger)

	var sources []PriceSource
	for _, name := range cfg.Sources {
		switch name {
		case "ebay":
			sources = append(sources, NewEbaySource(
				cfg.EbayAppIDEnv,
				cfg.EnableScraping,
				cfg.UserAgent,
				logger,
			))
		case "tcgplayer":
			sources = append(sources, NewTCGPlayerSource(
				cfg.UserAgent,
				logger,
			))
		case "pricecharting":
			sources = append(sources, NewPriceChartingSource(
				cfg.UserAgent,
				logger,
			))
		case "cardmarket":
			sources = append(sources, NewCardMarketSource(
				cfg.UserAgent,
				logger,
			))
		default:
			logger.Warn("unknown pricing source, skipping", "source", name)
		}
	}

	return &Pricer{
		sources:   sources,
		converter: converter,
		logger:    logger,
	}
}

// Close releases resources held by the Pricer, including the background
// currency rate refresh goroutine.
func (p *Pricer) Close() {
	if p.converter != nil {
		p.converter.Close()
	}
}

// FetchAllPrices concurrently fetches prices from all configured sources,
// converts non-USD prices to USD, and calculates a suggested price.
func (p *Pricer) FetchAllPrices(ctx context.Context, card CardIdentification) ([]PriceEstimate, *SuggestedPrice, error) {
	if len(p.sources) == 0 {
		return nil, nil, fmt.Errorf("no pricing sources configured")
	}

	type result struct {
		estimates []PriceEstimate
		err       error
		source    string
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results []result
	)

	for _, src := range p.sources {
		wg.Add(1)
		go func(s PriceSource) {
			defer wg.Done()
			estimates, err := s.FetchPrices(ctx, card)
			mu.Lock()
			results = append(results, result{
				estimates: estimates,
				err:       err,
				source:    s.Name(),
			})
			mu.Unlock()
		}(src)
	}

	wg.Wait()

	var allEstimates []PriceEstimate
	for _, r := range results {
		if r.err != nil {
			p.logger.Warn("source fetch failed", "source", r.source, "error", r.err)
			continue
		}
		allEstimates = append(allEstimates, r.estimates...)
	}

	if len(allEstimates) == 0 {
		return nil, nil, fmt.Errorf("no price estimates from any source")
	}

	// Convert all non-USD estimates to USD.
	usdPrices, err := p.normalizeToUSD(allEstimates)
	if err != nil {
		return allEstimates, nil, fmt.Errorf("currency conversion: %w", err)
	}

	suggested := calculateSuggestedPrice(usdPrices)
	return allEstimates, suggested, nil
}

// normalizeToUSD converts all estimates to USD. Estimates already in USD are
// passed through. If conversion fails for a non-USD estimate, an error is
// returned rather than silently using the unconverted value.
func (p *Pricer) normalizeToUSD(estimates []PriceEstimate) ([]float64, error) {
	prices := make([]float64, 0, len(estimates))
	for _, est := range estimates {
		if est.CurrencyCode == "USD" {
			prices = append(prices, est.Price)
			continue
		}
		converted, err := p.converter.ConvertToUSD(est.Price, est.CurrencyCode)
		if err != nil {
			return nil, fmt.Errorf("convert %s %.2f from %s: %w", est.Source, est.Price, est.CurrencyCode, err)
		}
		prices = append(prices, converted)
	}
	return prices, nil
}

// calculateSuggestedPrice computes pricing tiers from a set of USD prices.
// It uses the median price as the base and applies multipliers:
//   - QuickSell: 0.75x median
//   - FairValue: 1.0x median
//   - Premium: 1.25x median
//   - GradedValue: 1.5x median
func calculateSuggestedPrice(prices []float64) *SuggestedPrice {
	if len(prices) == 0 {
		return nil
	}

	median := computeMedian(prices)

	quickSell := roundCents(median * 0.75)
	fairValue := roundCents(median)
	premium := roundCents(median * 1.25)
	gradedValue := roundCents(median * 1.50)

	return &SuggestedPrice{
		QuickSell:      quickSell,
		FairValue:      fairValue,
		Premium:        premium,
		GradedValue:    gradedValue,
		QuickSellPrice: quickSell,
		Fair:           fairValue,
		Graded:         gradedValue,
		FairValueUSD:   fairValue,
		Currency:       "USD",
	}
}

// computeMedian returns the median of a slice of float64 values.
// The input slice is not modified.
func computeMedian(values []float64) float64 {
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	n := len(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}

// roundCents rounds a float64 to 2 decimal places.
func roundCents(v float64) float64 {
	return math.Round(v*100) / 100
}
