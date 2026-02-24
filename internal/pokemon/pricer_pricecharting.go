package pokemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

const (
	priceChartingSearchURL     = "https://www.pricecharting.com/search-products"
	priceChartingSourceTimeout = 15 * time.Second
)

var priceChartingPriceRe = regexp.MustCompile(`\$(\d+(?:\.\d{1,2})?)`)

// PriceChartingSource fetches price data by scraping PriceCharting search results.
type PriceChartingSource struct {
	userAgent string
	logger    *slog.Logger
	client    *http.Client
}

// NewPriceChartingSource creates a PriceChartingSource.
func NewPriceChartingSource(userAgent string, logger *slog.Logger) *PriceChartingSource {
	return &PriceChartingSource{
		userAgent: userAgent,
		logger:    logger,
		client:    &http.Client{Timeout: priceChartingSourceTimeout},
	}
}

func (p *PriceChartingSource) Name() string { return "pricecharting" }

// FetchPrices scrapes PriceCharting search for price values.
func (p *PriceChartingSource) FetchPrices(ctx context.Context, card CardIdentification) ([]PriceEstimate, error) {
	query := buildQuery(card)

	params := url.Values{
		"q":    {query},
		"type": {"prices"},
	}

	reqURL := priceChartingSearchURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("pricecharting request: %w", err)
	}
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pricecharting call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("pricecharting read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pricecharting status %d", resp.StatusCode)
	}

	return parsePriceChartingPrices(string(body)), nil
}

func parsePriceChartingPrices(html string) []PriceEstimate {
	matches := priceChartingPriceRe.FindAllStringSubmatch(html, -1)
	estimates := make([]PriceEstimate, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		price, err := strconv.ParseFloat(m[1], 64)
		if err != nil || price <= 0 {
			continue
		}
		estimates = append(estimates, PriceEstimate{
			Source:       "pricecharting",
			Price:        price,
			Currency:     "USD",
			CurrencyCode: "USD",
		})
	}
	return estimates
}
