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
	"strings"
	"time"
)

const (
	cardMarketSearchURL     = "https://www.cardmarket.com/en/Pokemon/Products/Search"
	cardMarketSourceTimeout = 15 * time.Second
)

// cardMarketPriceRe matches euro trend prices like "12,50 €" in HTML.
var cardMarketPriceRe = regexp.MustCompile(`(?i)trend[^€$]*?(\d+[.,]\d{1,2})\s*€`)

// CardMarketSource fetches price data by scraping CardMarket search results.
// Prices are returned in EUR.
type CardMarketSource struct {
	userAgent string
	logger    *slog.Logger
	client    *http.Client
}

// NewCardMarketSource creates a CardMarketSource.
func NewCardMarketSource(userAgent string, logger *slog.Logger) *CardMarketSource {
	return &CardMarketSource{
		userAgent: userAgent,
		logger:    logger,
		client:    &http.Client{Timeout: cardMarketSourceTimeout},
	}
}

func (c *CardMarketSource) Name() string { return "cardmarket" }

// FetchPrices scrapes CardMarket search for trend prices, returning EUR currency.
func (c *CardMarketSource) FetchPrices(ctx context.Context, card CardIdentification) ([]PriceEstimate, error) {
	query := buildQuery(card)

	params := url.Values{
		"searchString": {query},
	}

	reqURL := cardMarketSearchURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("cardmarket request: %w", err)
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cardmarket call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("cardmarket read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cardmarket status %d", resp.StatusCode)
	}

	return parseCardMarketPrices(string(body)), nil
}

func parseCardMarketPrices(html string) []PriceEstimate {
	matches := cardMarketPriceRe.FindAllStringSubmatch(html, -1)
	estimates := make([]PriceEstimate, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		// CardMarket uses comma as decimal separator (e.g. "12,50").
		priceStr := strings.Replace(m[1], ",", ".", 1)
		price, err := strconv.ParseFloat(priceStr, 64)
		if err != nil || price <= 0 {
			continue
		}
		estimates = append(estimates, PriceEstimate{
			Source:       "cardmarket",
			Price:        price,
			Currency:     "EUR",
			CurrencyCode: "EUR",
		})
	}
	return estimates
}
