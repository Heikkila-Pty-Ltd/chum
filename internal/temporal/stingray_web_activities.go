package temporal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

// WebVerifyActivity runs post-deploy web verification checks:
// HTTP smoke tests, Lighthouse audits, and broken link crawling.
func (a *Activities) WebVerifyActivity(ctx context.Context, req WebVerifyRequest) (*WebVerifyResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(webVerifyPrefix+" Starting web verification", "Project", req.Project, "URLs", len(req.URLs))

	if len(req.URLs) == 0 {
		return nil, fmt.Errorf("web verify: no URLs to check")
	}

	timeout := webVerifyHTTPTimeout
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}

	expectStatus := req.ExpectStatus
	if expectStatus == 0 {
		expectStatus = 200
	}

	result := &WebVerifyResult{
		Passed: true,
	}

	// --- 1. HTTP Smoke Tests ---
	for _, u := range req.URLs {
		check := checkURL(ctx, u, timeout, expectStatus, req.ExpectContains)
		result.HTTPChecks = append(result.HTTPChecks, check)

		if !check.Passed {
			result.Passed = false
			if check.Error != "" {
				result.Failures = append(result.Failures, fmt.Sprintf("HTTP check failed for %s: %s", u, check.Error))
			} else if check.StatusCode != check.ExpectedStatus {
				result.Failures = append(result.Failures, fmt.Sprintf("HTTP %s: expected status %d, got %d", u, check.ExpectedStatus, check.StatusCode))
			}
			for _, cm := range check.ContentMatches {
				if !cm.Found {
					result.Failures = append(result.Failures, fmt.Sprintf("HTTP %s: expected content not found: %q", u, cm.Expected))
				}
			}
		}

		logger.Info(webVerifyPrefix+" HTTP check",
			"URL", u,
			"Status", check.StatusCode,
			"ResponseTimeMs", check.ResponseTimeMs,
			"Passed", check.Passed,
		)
	}

	// --- 2. Lighthouse Audit ---
	if req.LighthouseEnabled && len(req.URLs) > 0 {
		// Run Lighthouse on the first URL
		targetURL := req.URLs[0]
		logger.Info(webVerifyPrefix+" Running Lighthouse audit", "URL", targetURL)

		lhCmd := runCommand(
			ctx,
			".", // workDir doesn't matter for lighthouse
			webVerifyLighthouseTimeout,
			"npx",
			"lighthouse",
			targetURL,
			"--output=json",
			"--chrome-flags=--headless --no-sandbox",
			"--quiet",
		)

		if lhCmd.Succeeded {
			lhResult, err := parseLighthouseJSON(lhCmd.Stdout)
			if err != nil {
				logger.Warn(webVerifyPrefix+" Failed to parse Lighthouse output", "error", err)
				result.Lighthouse = &LighthouseResult{
					URL:   targetURL,
					Error: err.Error(),
				}
			} else {
				lhResult.Passed = true

				if req.LighthouseMinPerf > 0 && lhResult.Performance < req.LighthouseMinPerf {
					lhResult.Passed = false
					lhResult.Failures = append(lhResult.Failures,
						fmt.Sprintf("performance score %d < minimum %d", lhResult.Performance, req.LighthouseMinPerf))
				}
				if req.LighthouseMinSEO > 0 && lhResult.SEO < req.LighthouseMinSEO {
					lhResult.Passed = false
					lhResult.Failures = append(lhResult.Failures,
						fmt.Sprintf("SEO score %d < minimum %d", lhResult.SEO, req.LighthouseMinSEO))
				}
				if req.LighthouseMinA11y > 0 && lhResult.Accessibility < req.LighthouseMinA11y {
					lhResult.Passed = false
					lhResult.Failures = append(lhResult.Failures,
						fmt.Sprintf("accessibility score %d < minimum %d", lhResult.Accessibility, req.LighthouseMinA11y))
				}

				if !lhResult.Passed {
					result.Passed = false
					result.Failures = append(result.Failures, lhResult.Failures...)
				}

				result.Lighthouse = lhResult

				logger.Info(webVerifyPrefix+" Lighthouse scores",
					"Performance", lhResult.Performance,
					"SEO", lhResult.SEO,
					"Accessibility", lhResult.Accessibility,
					"BestPractices", lhResult.BestPractices,
					"Passed", lhResult.Passed,
				)
			}
		} else {
			logger.Warn(webVerifyPrefix+" Lighthouse command failed",
				"ExitCode", lhCmd.ExitCode,
				"Stderr", truncate(lhCmd.Stderr, 500),
			)
			result.Lighthouse = &LighthouseResult{
				URL:   targetURL,
				Error: fmt.Sprintf("lighthouse exited %d: %s", lhCmd.ExitCode, truncate(lhCmd.Stderr, 200)),
			}
		}
	}

	// --- 3. Broken Link Crawl ---
	if req.CrawlBrokenLinks && len(req.URLs) > 0 {
		targetURL := req.URLs[0]
		logger.Info(webVerifyPrefix+" Crawling links", "URL", targetURL)

		// Fetch the page body for link extraction
		client := &http.Client{Timeout: timeout}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err == nil {
			httpReq.Header.Set("User-Agent", "CHUM-StingrayWeb/1.0")
			resp, err := client.Do(httpReq)
			if err == nil {
				bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
				resp.Body.Close()

				broken := crawlLinks(ctx, targetURL, bodyBytes, webVerifyCrawlTimeout)
				result.BrokenLinks = broken

				if len(broken) > 0 {
					result.Passed = false
					for _, bl := range broken {
						msg := fmt.Sprintf("broken link: %s (status %d, from %s)", bl.URL, bl.StatusCode, bl.Referrer)
						if bl.Error != "" {
							msg = fmt.Sprintf("broken link: %s (error: %s, from %s)", bl.URL, bl.Error, bl.Referrer)
						}
						result.Failures = append(result.Failures, msg)
					}
				}

				logger.Info(webVerifyPrefix+" Link crawl complete",
					"InternalLinks", len(broken),
					"BrokenCount", len(broken),
				)
			} else {
				logger.Warn(webVerifyPrefix+" Failed to fetch page for link crawl", "error", err)
			}
		}
	}

	logMsg := "PASS"
	if !result.Passed {
		logMsg = "FAIL"
	}
	logger.Info(webVerifyPrefix+" Web verification "+logMsg,
		"Project", req.Project,
		"Passed", result.Passed,
		"Failures", strings.Join(result.Failures, "; "),
	)

	return result, nil
}
