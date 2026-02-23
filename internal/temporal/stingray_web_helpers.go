package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// checkURL performs an HTTP GET and validates status + content assertions.
func checkURL(ctx context.Context, targetURL string, timeout time.Duration, expectStatus int, expectContains []string) HTTPCheckResult {
	if expectStatus == 0 {
		expectStatus = 200
	}

	result := HTTPCheckResult{
		URL:            targetURL,
		ExpectedStatus: expectStatus,
	}

	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		result.Error = fmt.Sprintf("create request: %v", err)
		return result
	}
	req.Header.Set("User-Agent", "CHUM-StingrayWeb/1.0")

	start := time.Now()
	resp, err := client.Do(req)
	result.ResponseTimeMs = time.Since(start).Milliseconds()

	if err != nil {
		result.Error = fmt.Sprintf("HTTP GET: %v", err)
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Passed = resp.StatusCode == expectStatus

	// Read body for content assertions (cap at 2MB to avoid OOM)
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		result.Error = fmt.Sprintf("read body: %v", err)
		result.Passed = false
		return result
	}
	bodyStr := string(bodyBytes)

	for _, needle := range expectContains {
		match := ContentMatch{
			Expected: needle,
			Found:    strings.Contains(bodyStr, needle),
		}
		result.ContentMatches = append(result.ContentMatches, match)
		if !match.Found {
			result.Passed = false
		}
	}

	return result
}

// parseLighthouseJSON extracts category scores from Lighthouse JSON output.
func parseLighthouseJSON(raw string) (*LighthouseResult, error) {
	// Lighthouse JSON has structure: { categories: { performance: { score: 0.85 }, ... } }
	var lhOutput struct {
		Categories struct {
			Performance struct {
				Score *float64 `json:"score"`
			} `json:"performance"`
			SEO struct {
				Score *float64 `json:"score"`
			} `json:"seo"`
			Accessibility struct {
				Score *float64 `json:"score"`
			} `json:"accessibility"`
			BestPractices struct {
				Score *float64 `json:"score"`
			} `json:"best-practices"`
		} `json:"categories"`
		RequestedURL string `json:"requestedUrl"`
	}

	if err := json.Unmarshal([]byte(raw), &lhOutput); err != nil {
		return nil, fmt.Errorf("parse lighthouse JSON: %w", err)
	}

	result := &LighthouseResult{
		URL: lhOutput.RequestedURL,
	}

	if s := lhOutput.Categories.Performance.Score; s != nil {
		result.Performance = int(*s * 100)
	}
	if s := lhOutput.Categories.SEO.Score; s != nil {
		result.SEO = int(*s * 100)
	}
	if s := lhOutput.Categories.Accessibility.Score; s != nil {
		result.Accessibility = int(*s * 100)
	}
	if s := lhOutput.Categories.BestPractices.Score; s != nil {
		result.BestPractices = int(*s * 100)
	}

	return result, nil
}

// hrefRegex matches href attributes in anchor tags.
var hrefRegex = regexp.MustCompile(`<a\s[^>]*href=["']([^"']+)["']`)

// crawlLinks extracts internal links from HTML and checks each for broken status.
func crawlLinks(ctx context.Context, baseURL string, body []byte, timeout time.Duration) []BrokenLinkResult {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return []BrokenLinkResult{{URL: baseURL, Error: err.Error()}}
	}

	matches := hrefRegex.FindAllSubmatch(body, -1)
	seen := make(map[string]bool)
	var results []BrokenLinkResult

	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	for _, match := range matches {
		href := string(match[1])
		if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") || strings.HasPrefix(href, "javascript:") {
			continue
		}

		// Resolve relative URLs
		resolved, err := url.Parse(href)
		if err != nil {
			continue
		}
		absolute := parsed.ResolveReference(resolved)

		// Only check internal links (same host)
		if absolute.Host != parsed.Host {
			continue
		}

		fullURL := absolute.String()
		if seen[fullURL] {
			continue
		}
		seen[fullURL] = true

		// HEAD request to check link
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, fullURL, nil)
		if err != nil {
			results = append(results, BrokenLinkResult{
				URL:      fullURL,
				Referrer: baseURL,
				Error:    err.Error(),
			})
			continue
		}
		req.Header.Set("User-Agent", "CHUM-StingrayWeb/1.0")

		resp, err := client.Do(req)
		if err != nil {
			results = append(results, BrokenLinkResult{
				URL:      fullURL,
				Referrer: baseURL,
				Error:    err.Error(),
			})
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			results = append(results, BrokenLinkResult{
				URL:        fullURL,
				StatusCode: resp.StatusCode,
				Referrer:   baseURL,
			})
		}
	}

	return results
}
