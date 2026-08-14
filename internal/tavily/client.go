package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	apiKey     string
	httpClient *http.Client
}

type SearchRequest struct {
	APIKey        string `json:"api_key"`
	Query         string `json:"query"`
	IncludeAnswer bool   `json:"include_answer"`
}

type SearchResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

type SearchResponse struct {
	Answer  string         `json:"answer"`
	Results []SearchResult `json:"results"`
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Search performs a web search using Tavily API.
func (c *Client) Search(ctx context.Context, query string) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("tavily API key is not configured")
	}

	reqBody := SearchRequest{
		APIKey:        c.apiKey,
		Query:         query,
		IncludeAnswer: true,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("tavily API error %d: %s", resp.StatusCode, string(b))
	}

	var searchResp SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Use the AI summarized answer if available
	if searchResp.Answer != "" {
		return "Tavily Search Summary:\n" + searchResp.Answer, nil
	}

	// Fallback to formatting raw snippets
	if len(searchResp.Results) == 0 {
		return "No search results found.", nil
	}

	var sb strings.Builder
	sb.WriteString("Tavily Search Results:\n\n")
	for i, r := range searchResp.Results {
		if i >= 3 {
			break // Limit to top 3 snippets if there's no overall answer
		}
		sb.WriteString(fmt.Sprintf("%d. [%s](%s)\n%s\n\n", i+1, r.Title, r.URL, r.Content))
	}
	return sb.String(), nil
}
