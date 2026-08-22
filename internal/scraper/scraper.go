package scraper

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	html2md "github.com/JohannesKaufmann/html-to-markdown/v2"
	"codeberg.org/readeck/go-readability/v2"
)

// FetchAndExtractText takes a URL, fetches it, parses it with go-readability to extract the main article,
// and converts the HTML into Markdown.
func FetchAndExtractText(ctx context.Context, url string) (string, error) {
	// Create a short-lived context if not provided, but usually we pass the orchestrator ctx
	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	
	// Set a standard user agent to avoid being blocked by simple scrapers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("received non-200 status code: %d", resp.StatusCode)
	}

	// Parse with go-readability
	article, err := readability.FromReader(resp.Body, req.URL)
	if err != nil {
		return "", fmt.Errorf("failed to parse article with readability: %w", err)
	}

	// If readability didn't find much, we can fall back to raw body or just return what we have.
	// But usually it finds something.
	var buf strings.Builder
	if err := article.RenderHTML(&buf); err != nil {
		return "", fmt.Errorf("failed to render readable html: %w", err)
	}
	htmlContent := buf.String()

	if strings.TrimSpace(htmlContent) == "" {
		return "", fmt.Errorf("no readable content found on the page")
	}

	// Convert the readable HTML to Markdown
	md, err := html2md.ConvertString(htmlContent)
	if err != nil {
		return "", fmt.Errorf("failed to convert html to markdown: %w", err)
	}

	// Prepend the title for context
	finalText := fmt.Sprintf("# %s\n\n%s", article.Title(), md)
	
	// Limit to ~20k characters to prevent blowing up the LLM context window
	// (Gemini Pro can handle much more, but it's good practice)
	const maxLen = 20000
	if len(finalText) > maxLen {
		finalText = finalText[:maxLen] + "\n\n... [Content Truncated due to length]"
	}

	return finalText, nil
}
