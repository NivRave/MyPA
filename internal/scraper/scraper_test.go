package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchAndExtractText(t *testing.T) {
	htmlContent := `
		<html>
			<head><title>Test Article Title</title></head>
			<body>
				<h1>Test Article Title</h1>
				<article>
					<p>This is the main readable content of the article.</p>
					<p>It has multiple paragraphs.</p>
				</article>
				<nav>Ignore this navigation</nav>
			</body>
		</html>
	`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36", r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(htmlContent))
	}))
	defer ts.Close()

	text, err := FetchAndExtractText(context.Background(), ts.URL)
	require.NoError(t, err)

	assert.Contains(t, text, "# Test Article Title")
	assert.Contains(t, text, "This is the main readable content of the article.")
	assert.Contains(t, text, "It has multiple paragraphs.")
	assert.NotContains(t, text, "Ignore this navigation")
}

func TestFetchAndExtractText_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	_, err := FetchAndExtractText(context.Background(), ts.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "received non-200 status code: 404")
}

func TestFetchAndExtractText_NoContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body></body></html>`)) // Empty body
	}))
	defer ts.Close()

	_, err := FetchAndExtractText(context.Background(), ts.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to render readable html: the Node field is nil")
}

func TestFetchAndExtractText_Truncation(t *testing.T) {
	// Create a very large HTML document
	var sb strings.Builder
	sb.WriteString("<html><head><title>Large</title></head><body><article>")
	for i := 0; i < 3000; i++ {
		sb.WriteString("<p>This is a long sentence that will be repeated many times to exceed the 20000 character limit.</p>")
	}
	sb.WriteString("</article></body></html>")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(sb.String()))
	}))
	defer ts.Close()

	text, err := FetchAndExtractText(context.Background(), ts.URL)
	require.NoError(t, err)

	// Length should be 20000 + length of truncation message
	truncMsg := "\n\n... [Content Truncated due to length]"
	assert.Equal(t, 20000+len(truncMsg), len(text))
	assert.True(t, strings.HasSuffix(text, truncMsg))
}
