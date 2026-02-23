package temporal

import "time"

// Stingray Web timeout and output constants.
const (
	webVerifyHTTPTimeout    = 30 * time.Second
	webVerifyLighthouseTimeout = 90 * time.Second
	webVerifyCrawlTimeout   = 60 * time.Second
	webVerifyPrefix         = "\033[36m🌊 STINGRAY-WEB\033[0m"
)

// WebVerifyRequest is the input for WebVerifyActivity.
type WebVerifyRequest struct {
	Project           string   `json:"project"`
	URLs              []string `json:"urls"`
	ExpectStatus      int      `json:"expect_status"`       // default 200
	ExpectContains    []string `json:"expect_contains"`     // strings that must appear in body
	LighthouseEnabled bool     `json:"lighthouse_enabled"`
	LighthouseMinPerf int      `json:"lighthouse_min_perf"` // 0-100
	LighthouseMinSEO  int      `json:"lighthouse_min_seo"`
	LighthouseMinA11y int      `json:"lighthouse_min_a11y"`
	CrawlBrokenLinks  bool     `json:"crawl_broken_links"`
	TimeoutSeconds    int      `json:"timeout_seconds"`
}

// WebVerifyResult is the top-level result from WebVerifyActivity.
type WebVerifyResult struct {
	Passed      bool              `json:"passed"`
	Failures    []string          `json:"failures"`
	HTTPChecks  []HTTPCheckResult `json:"http_checks"`
	Lighthouse  *LighthouseResult `json:"lighthouse,omitempty"`
	BrokenLinks []BrokenLinkResult `json:"broken_links,omitempty"`
}

// HTTPCheckResult captures a single URL smoke test.
type HTTPCheckResult struct {
	URL             string   `json:"url"`
	StatusCode      int      `json:"status_code"`
	ExpectedStatus  int      `json:"expected_status"`
	ResponseTimeMs  int64    `json:"response_time_ms"`
	Passed          bool     `json:"passed"`
	ContentMatches  []ContentMatch `json:"content_matches,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// ContentMatch reports whether a required string was found in the response.
type ContentMatch struct {
	Expected string `json:"expected"`
	Found    bool   `json:"found"`
}

// LighthouseResult holds parsed Lighthouse audit scores.
type LighthouseResult struct {
	URL             string  `json:"url"`
	Performance     int     `json:"performance"`      // 0-100
	SEO             int     `json:"seo"`
	Accessibility   int     `json:"accessibility"`
	BestPractices   int     `json:"best_practices"`
	Passed          bool    `json:"passed"`
	Failures        []string `json:"failures,omitempty"`
	RawOutput       string  `json:"raw_output,omitempty"` // truncated
	Error           string  `json:"error,omitempty"`
}

// BrokenLinkResult represents a broken link found during crawling.
type BrokenLinkResult struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	Referrer   string `json:"referrer"` // page where the link was found
	Error      string `json:"error,omitempty"`
}
