package pokemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ebayFindingAPIURL = "https://svcs.ebay.com/services/search/FindingService/v1"
	ebaySearchURL     = "https://www.ebay.com/sch/i.html"
	ebaySourceTimeout = 15 * time.Second
	// maxSourceResponseBytes limits HTML/API response body size for source scraping.
	maxSourceResponseBytes int64 = 2 << 20 // 2 MiB
)

// EbaySource fetches price data from eBay via the Finding API or HTML scraping.
type EbaySource struct {
	appID     string // eBay Finding API app ID; empty means API is unavailable
	scrape    bool   // whether HTML scraping is enabled
	userAgent string
	logger    *slog.Logger
	client    *http.Client
}

// NewEbaySource creates an EbaySource. It reads the eBay app ID from the
// environment variable named by appIDEnv. If empty or unset, the API path
// is disabled and only scraping is used (when enableScraping is true).
func NewEbaySource(appIDEnv string, enableScraping bool, userAgent string, logger *slog.Logger) *EbaySource {
	var appID string
	if appIDEnv != "" {
		appID = strings.TrimSpace(os.Getenv(appIDEnv))
	}
	return &EbaySource{
		appID:     appID,
		scrape:    enableScraping,
		userAgent: userAgent,
		logger:    logger,
		client:    &http.Client{Timeout: ebaySourceTimeout},
	}
}

func (e *EbaySource) Name() string { return "ebay" }

// FetchPrices returns price estimates from eBay. If an API key is available
// the Finding API is used; otherwise HTML scraping of sold listings is attempted
// when scraping is enabled.
func (e *EbaySource) FetchPrices(ctx context.Context, card CardIdentification) ([]PriceEstimate, error) {
	if e.appID != "" {
		return e.fetchAPI(ctx, card)
	}
	if e.scrape {
		return e.fetchScrape(ctx, card)
	}
	return nil, fmt.Errorf("ebay: no API key and scraping disabled")
}

// buildQuery constructs a search query string for a card.
// The Variant field is skipped for Unlimited cards.
func buildQuery(card CardIdentification) string {
	parts := []string{card.CardName, card.SetName}
	if card.SetNumber != "" {
		parts = append(parts, card.SetNumber)
	}
	if card.Variant != "" && !strings.EqualFold(card.Variant, "Unlimited") {
		parts = append(parts, card.Variant)
	}
	return strings.Join(parts, " ")
}

// fetchAPI uses the eBay Finding API to get completed item prices.
func (e *EbaySource) fetchAPI(ctx context.Context, card CardIdentification) ([]PriceEstimate, error) {
	query := buildQuery(card)

	params := url.Values{
		"OPERATION-NAME":                 {"findCompletedItems"},
		"SERVICE-VERSION":                {"1.13.0"},
		"SECURITY-APPNAME":               {e.appID},
		"RESPONSE-DATA-FORMAT":           {"JSON"},
		"keywords":                       {query},
		"categoryId":                     {"183454"},
		"sortOrder":                      {"EndTimeSoonest"},
		"paginationInput.entriesPerPage": {"10"},
	}

	reqURL := ebayFindingAPIURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("ebay API request: %w", err)
	}
	if e.userAgent != "" {
		req.Header.Set("User-Agent", e.userAgent)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ebay API call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("ebay API read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ebay API status %d: %.200s", resp.StatusCode, string(body))
	}

	return parseEbayAPIResponse(body)
}

// ebayAPIResponse is the minimal structure for the eBay Finding API JSON response.
type ebayAPIResponse struct {
	FindCompletedItemsResponse []struct {
		SearchResult []struct {
			Item []struct {
				Title         []string `json:"title"`
				SellingStatus []struct {
					CurrentPrice []struct {
						Value      string `json:"__value__"`
						CurrencyID string `json:"@currencyId"`
					} `json:"currentPrice"`
				} `json:"sellingStatus"`
				ViewItemURL []string `json:"viewItemURL"`
			} `json:"item"`
		} `json:"searchResult"`
	} `json:"findCompletedItemsResponse"`
}

func parseEbayAPIResponse(data []byte) ([]PriceEstimate, error) {
	var resp ebayAPIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse ebay API JSON: %w", err)
	}

	var estimates []PriceEstimate
	for _, r := range resp.FindCompletedItemsResponse {
		for _, sr := range r.SearchResult {
			for _, item := range sr.Item {
				for _, ss := range item.SellingStatus {
					for _, cp := range ss.CurrentPrice {
						price, err := strconv.ParseFloat(cp.Value, 64)
						if err != nil || price <= 0 {
							continue
						}
						est := PriceEstimate{
							Source:       "ebay",
							Price:        price,
							Currency:     cp.CurrencyID,
							CurrencyCode: cp.CurrencyID,
						}
						if len(item.ViewItemURL) > 0 {
							est.SourceURL = item.ViewItemURL[0]
						}
						estimates = append(estimates, est)
					}
				}
			}
		}
	}
	return estimates, nil
}

// fetchScrape scrapes eBay sold listings HTML for prices.
func (e *EbaySource) fetchScrape(ctx context.Context, card CardIdentification) ([]PriceEstimate, error) {
	query := buildQuery(card)

	params := url.Values{
		"_nkw":        {query},
		"_sacat":      {"183454"},
		"LH_Sold":     {"1"},
		"LH_Complete": {"1"},
	}

	reqURL := ebaySearchURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("ebay scrape request: %w", err)
	}
	if e.userAgent != "" {
		req.Header.Set("User-Agent", e.userAgent)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ebay scrape call: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("ebay scrape read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ebay scrape status %d", resp.StatusCode)
	}

	return parseEbayScrapeHTML(string(body)), nil
}

// parseEbayScrapeHTML extracts prices from eBay sold listing HTML using regexp.
// Looks for s-item__price spans containing dollar amounts.
var ebayItemPriceBlockRe = regexp.MustCompile(`s-item__price[^>]*>[^<]*\$(\d+(?:\.\d{1,2})?)`)

func parseEbayScrapeHTML(html string) []PriceEstimate {
	matches := ebayItemPriceBlockRe.FindAllStringSubmatch(html, -1)
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
			Source:       "ebay",
			Price:        price,
			Currency:     "USD",
			CurrencyCode: "USD",
		})
	}
	return estimates
}
