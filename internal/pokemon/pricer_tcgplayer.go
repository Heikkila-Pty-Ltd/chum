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
	tcgPlayerSearchURL     = "https://www.tcgplayer.com/search/pokemon/product"
	tcgPlayerSourceTimeout = 15 * time.Second
)

var tcgPriceRe = regexp.MustCompile(`(?i)market\s*price[^$]*\$(\d+(?:\.\d{1,2})?)`)

// TCGPlayerSource fetches price data by scraping TCGPlayer search results.
type TCGPlayerSource struct {
	userAgent string
	logger    *slog.Logger
	client    *http.Client
}

// NewTCGPlayerSource creates a TCGPlayerSource.
func NewTCGPlayerSource(userAgent string, logger *slog.Logger) *TCGPlayerSource {
	return &TCGPlayerSource{
		userAgent: userAgent,
		logger:    logger,
		client:    &http.Client{Timeout: tcgPlayerSourceTimeout},
	}
}

func (t *TCGPlayerSource) Name() string { return "tcgplayer" }

// FetchPrices scrapes TCGPlayer search results for market price data.
func (t *TCGPlayerSource) FetchPrices(ctx context.Context, card CardIdentification) ([]PriceEstimate, error) {
	query := buildQuery(card)

	params := url.Values{
		"q":               {query},
		"productLineName": {"pokemon"},
	}

	reqURL := tcgPlayerSearchURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("tcgplayer request: %w", err)
	}
	if t.userAgent != "" {
		req.Header.Set("User-Agent", t.userAgent)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tcgplayer call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("tcgplayer read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tcgplayer status %d", resp.StatusCode)
	}

	return parseTCGPrices(string(body)), nil
}

func parseTCGPrices(html string) []PriceEstimate {
	matches := tcgPriceRe.FindAllStringSubmatch(html, -1)
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
			Source:       "tcgplayer",
			Price:        price,
			Currency:     "USD",
			CurrencyCode: "USD",
		})
	}
	return estimates
}
