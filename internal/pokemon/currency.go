package pokemon

import (
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	ecbRatesURL      = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"
	ecbFetchTimeout  = 10 * time.Second
	ecbRefreshPeriod = 24 * time.Hour
	// maxECBResponseBytes limits the ECB XML response size to prevent OOM.
	maxECBResponseBytes int64 = 1 << 20 // 1 MiB
)

// ecbEnvelope is the top-level XML structure from the ECB daily rates feed.
type ecbEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Cube    ecbOuter `xml:"Cube"`
}

type ecbOuter struct {
	Cube ecbInner `xml:"Cube"`
}

type ecbInner struct {
	Rates []ecbRate `xml:"Cube"`
}

type ecbRate struct {
	Currency string `xml:"currency,attr"`
	Rate     string `xml:"rate,attr"`
}

// CurrencyConverter converts between currencies using ECB exchange rates.
// All rates are stored as USD-per-unit (i.e. how many USD you get for 1 unit).
type CurrencyConverter struct {
	mu     sync.RWMutex
	rates  map[string]float64
	logger *slog.Logger
	client *http.Client
	done   chan struct{}
}

// NewCurrencyConverter creates a converter pre-populated with USD=1.0
// and performs a synchronous initial rate fetch before returning.
// A background goroutine refreshes rates every 24 hours.
// Call Close() to stop the background goroutine.
func NewCurrencyConverter(logger *slog.Logger) *CurrencyConverter {
	if logger == nil {
		logger = slog.Default()
	}
	cc := &CurrencyConverter{
		rates:  map[string]float64{"USD": 1.0},
		logger: logger,
		client: &http.Client{Timeout: ecbFetchTimeout},
		done:   make(chan struct{}),
	}

	// Synchronous initial fetch so rates are available before first use.
	// This eliminates the startup race condition where GetRate would fail
	// for EUR before the async goroutine completes.
	if err := cc.refresh(); err != nil {
		cc.logger.Warn("initial currency rate fetch failed, will retry in background", "error", err)
	}

	go cc.backgroundRefresh()
	return cc
}

// Close stops the background refresh goroutine.
func (cc *CurrencyConverter) Close() {
	select {
	case <-cc.done:
		// Already closed.
	default:
		close(cc.done)
	}
}

func (cc *CurrencyConverter) backgroundRefresh() {
	ticker := time.NewTicker(ecbRefreshPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-cc.done:
			return
		case <-ticker.C:
			if err := cc.refresh(); err != nil {
				cc.logger.Warn("currency rate refresh failed", "error", err)
			}
		}
	}
}

// refresh fetches the latest ECB rates and rebuilds the rate map.
// ECB rates are EUR-based; we convert to USD-based by dividing by the EUR/USD rate.
func (cc *CurrencyConverter) refresh() error {
	resp, err := cc.client.Get(ecbRatesURL)
	if err != nil {
		return fmt.Errorf("fetch ECB rates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ECB rates returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxECBResponseBytes))
	if err != nil {
		return fmt.Errorf("read ECB response: %w", err)
	}

	var envelope ecbEnvelope
	if err := xml.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("parse ECB XML: %w", err)
	}

	// Parse EUR-based rates.
	eurRates := make(map[string]float64, len(envelope.Cube.Cube.Rates)+1)
	eurRates["EUR"] = 1.0
	for _, r := range envelope.Cube.Cube.Rates {
		val, err := strconv.ParseFloat(r.Rate, 64)
		if err != nil || val <= 0 {
			continue
		}
		eurRates[r.Currency] = val
	}

	eurToUSD, ok := eurRates["USD"]
	if !ok || eurToUSD <= 0 {
		return fmt.Errorf("USD rate not found in ECB data")
	}

	// Convert EUR-base rates to USD-base rates.
	// For currency X with EUR-rate R (meaning 1 EUR = R X):
	//   1 X = 1/R EUR = eurToUSD/R USD
	// So usdPerUnit = eurToUSD / R
	newRates := make(map[string]float64, len(eurRates))
	newRates["USD"] = 1.0
	for currency, eurRate := range eurRates {
		if currency == "USD" {
			continue
		}
		newRates[currency] = eurToUSD / eurRate
	}

	cc.mu.Lock()
	cc.rates = newRates
	cc.mu.Unlock()

	cc.logger.Info("currency rates refreshed", "count", len(newRates))
	return nil
}

// GetRate returns how many USD you get for 1 unit of the given currency.
func (cc *CurrencyConverter) GetRate(currency string) (float64, error) {
	cc.mu.RLock()
	rate, ok := cc.rates[currency]
	cc.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("no exchange rate for %s", currency)
	}
	return rate, nil
}

// ConvertToUSD converts an amount in the given currency to USD.
func (cc *CurrencyConverter) ConvertToUSD(amount float64, currency string) (float64, error) {
	if currency == "USD" {
		return amount, nil
	}
	rate, err := cc.GetRate(currency)
	if err != nil {
		return 0, err
	}
	return math.Round(amount*rate*100) / 100, nil
}
