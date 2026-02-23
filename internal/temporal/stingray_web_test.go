package temporal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckURL_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprint(w, "<html><body>Golf Concierge Asia</body></html>")
	}))
	defer srv.Close()

	result := checkURL(context.Background(), srv.URL, 5*time.Second, 200, []string{"Golf Concierge"})
	assert.True(t, result.Passed)
	assert.Equal(t, 200, result.StatusCode)
	assert.Equal(t, 200, result.ExpectedStatus)
	assert.Empty(t, result.Error)
	require.Len(t, result.ContentMatches, 1)
	assert.True(t, result.ContentMatches[0].Found)
	assert.True(t, result.ResponseTimeMs >= 0)
}

func TestCheckURL_WrongStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	result := checkURL(context.Background(), srv.URL, 5*time.Second, 200, nil)
	assert.False(t, result.Passed)
	assert.Equal(t, 404, result.StatusCode)
}

func TestCheckURL_ContentNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprint(w, "<html><body>Something else</body></html>")
	}))
	defer srv.Close()

	result := checkURL(context.Background(), srv.URL, 5*time.Second, 200, []string{"Golf Concierge"})
	assert.False(t, result.Passed)
	require.Len(t, result.ContentMatches, 1)
	assert.False(t, result.ContentMatches[0].Found)
}

func TestCheckURL_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // will exceed timeout
		w.WriteHeader(200)
	}))
	defer srv.Close()

	result := checkURL(context.Background(), srv.URL, 100*time.Millisecond, 200, nil)
	assert.False(t, result.Passed)
	assert.NotEmpty(t, result.Error)
}

func TestCheckURL_DefaultStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// expectStatus=0 should default to 200
	result := checkURL(context.Background(), srv.URL, 5*time.Second, 0, nil)
	assert.True(t, result.Passed)
	assert.Equal(t, 200, result.ExpectedStatus)
}

func TestParseLighthouseJSON(t *testing.T) {
	lhJSON := `{
		"requestedUrl": "https://example.com",
		"categories": {
			"performance": {"score": 0.85},
			"seo": {"score": 0.92},
			"accessibility": {"score": 0.78},
			"best-practices": {"score": 0.95}
		}
	}`

	result, err := parseLighthouseJSON(lhJSON)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com", result.URL)
	assert.Equal(t, 85, result.Performance)
	assert.Equal(t, 92, result.SEO)
	assert.Equal(t, 78, result.Accessibility)
	assert.Equal(t, 95, result.BestPractices)
}

func TestParseLighthouseJSON_MissingCategories(t *testing.T) {
	lhJSON := `{"requestedUrl": "https://example.com", "categories": {}}`

	result, err := parseLighthouseJSON(lhJSON)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Performance)
	assert.Equal(t, 0, result.SEO)
}

func TestParseLighthouseJSON_Invalid(t *testing.T) {
	_, err := parseLighthouseJSON("not json")
	assert.Error(t, err)
}

func TestCrawlLinks_FindsBrokenLinks(t *testing.T) {
	// Create a server that returns 404 for /broken but 200 for everything else
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken" {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	body := fmt.Sprintf(`<html>
		<a href="/good">Good</a>
		<a href="/broken">Broken</a>
		<a href="https://external.com/skip">External</a>
		<a href="#anchor">Anchor</a>
		<a href="mailto:test@test.com">Email</a>
	</html>`)

	broken := crawlLinks(context.Background(), srv.URL, []byte(body), 5*time.Second)
	require.Len(t, broken, 1)
	assert.Contains(t, broken[0].URL, "/broken")
	assert.Equal(t, 404, broken[0].StatusCode)
}

func TestCrawlLinks_NoBrokenLinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	body := fmt.Sprintf(`<html><a href="/ok">OK</a></html>`)

	broken := crawlLinks(context.Background(), srv.URL, []byte(body), 5*time.Second)
	assert.Empty(t, broken)
}

func TestCrawlLinks_SkipsExternalLinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	body := `<html><a href="https://other-domain.com/page">External</a></html>`

	broken := crawlLinks(context.Background(), srv.URL, []byte(body), 5*time.Second)
	assert.Empty(t, broken)
}

func TestCrawlLinks_DeduplicatesLinks(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/page" {
			calls++
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	body := `<html>
		<a href="/page">First</a>
		<a href="/page">Duplicate</a>
	</html>`

	crawlLinks(context.Background(), srv.URL, []byte(body), 5*time.Second)
	assert.Equal(t, 1, calls, "should only HEAD each unique URL once")
}
