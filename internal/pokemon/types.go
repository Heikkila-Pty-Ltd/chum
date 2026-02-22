package pokemon

// PricingConfig holds pricing source settings.
type PricingConfig struct {
	Sources        []string `json:"sources" toml:"sources"`
	EbayAppIDEnv   string   `json:"ebay_app_id_env" toml:"ebay_app_id_env"`
	EnableScraping bool     `json:"enable_scraping" toml:"enable_scraping"`
	UserAgent      string   `json:"user_agent" toml:"user_agent"`
}

// PriceEstimate holds a single price data point from a source.
type PriceEstimate struct {
	Source       string  `json:"source"`
	Price        float64 `json:"price"`
	Currency     string  `json:"currency"`
	CurrencyCode string  `json:"currency_code"`
	SourceURL    string  `json:"source_url,omitempty"`
}

// SuggestedPrice holds calculated pricing tiers.
type SuggestedPrice struct {
	QuickSell      float64 `json:"quick_sell"`
	FairValue      float64 `json:"fair_value"`
	Premium        float64 `json:"premium"`
	GradedValue    float64 `json:"graded_value"`
	QuickSellPrice float64 `json:"quick_sell_price"`
	Fair           float64 `json:"fair"`
	Graded         float64 `json:"graded"`
	FairValueUSD   float64 `json:"fair_value_usd"`
	Currency       string  `json:"currency"`
}
