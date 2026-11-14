package web_research

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nivik/mypa/internal/domains/toolutil"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/scraper"
	"google.golang.org/genai"
)

// Option configures the web_research Module.
type Option func(*Module)

// WithClient sets a static TavilyClient.
func WithClient(client orchestrator.TavilyClient) Option {
	return func(m *Module) {
		m.client = client
	}
}

// WithClientProvider sets a dynamic TavilyClient provider function.
func WithClientProvider(provider func() orchestrator.TavilyClient) Option {
	return func(m *Module) {
		m.clientProvider = provider
	}
}

// Module implements orchestrator.ToolModule for web search and scraping.
type Module struct {
	client         orchestrator.TavilyClient
	clientProvider func() orchestrator.TavilyClient
}

// NewModule initializes the web_research module.
func NewModule(opts ...Option) *Module {
	m := &Module{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *Module) Name() string {
	return "web_research"
}

func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	return ""
}

func (m *Module) getClient() orchestrator.TavilyClient {
	if m.clientProvider != nil {
		return m.clientProvider()
	}
	return m.client
}

func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "search_web",
			Description: "Searches the live web using Tavily for current news, facts, recent events, or information you don't know.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"query": {
						Type:        genai.TypeString,
						Description: "The search query to look up on the web.",
					},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "fetch_webpage",
			Description: "Fetches and returns the main article text content of a webpage. Use this when the user sends a link and wants you to read, summarize, or save it.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"url": {
						Type:        genai.TypeString,
						Description: "The full URL of the webpage to fetch.",
					},
				},
				Required: []string{"url"},
			},
		},
	}
}

func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	actorID := callCtx.ActorID()

	switch toolName {
	case "search_web":
		client := m.getClient()
		if client == nil {
			return "⚠️ Web search integration is not configured.", nil
		}

		query := toolutil.String(args, "query")
		if query == "" {
			return "Missing query parameter", nil
		}

		slog.Info("executing search_web tool", "user", actorID, "query", query)
		if callCtx.SendNotification != nil {
			_ = callCtx.SendNotification(ctx, actorID, "🔍 Searching the web...")
		}

		result, err := client.Search(ctx, query)
		if err != nil {
			slog.Error("search_web failed", "error", err)
			return fmt.Sprintf("Error searching the web: %v", err), nil
		}
		return result, nil

	case "fetch_webpage":
		url := toolutil.String(args, "url")
		if url == "" {
			return "Missing url parameter", nil
		}

		slog.Info("executing fetch_webpage tool", "user", actorID, "url", url)
		if callCtx.SendNotification != nil {
			_ = callCtx.SendNotification(ctx, actorID, "📖 Reading webpage...")
		}

		content, err := scraper.FetchAndExtractText(ctx, url)
		if err != nil {
			slog.Error("fetch_webpage failed", "error", err, "url", url)
			return fmt.Sprintf("Error fetching webpage: %v", err), nil
		}

		if secState := orchestrator.GetTurnSecurityState(ctx); secState != nil {
			secState.MarkUntrusted("webpage:" + url)
		}
		wrappedContent := orchestrator.WrapUntrustedContent("webpage", url, content)

		summaryPrompt := fmt.Sprintf("Here is the content of the webpage:\n\n%s\n\nPlease fulfill the user's original request using this information.\nIf they asked to save it for later, use the create_task tool.\nIf they asked for a summary, provide a comprehensive, detailed summary. Use explicit **bold** markdown (double asterisks) for all topics and section headers (do not use single asterisks).", wrappedContent)

		if callCtx.LLM != nil {
			summaryResp, err := callCtx.LLM.Chat(ctx, "", nil, summaryPrompt, nil, "")
			if err != nil {
				return "❌ Failed to process webpage: " + err.Error(), nil
			}
			return summaryResp.Text, nil
		}

		return wrappedContent, nil

	default:
		return "", fmt.Errorf("unknown tool %s in web_research", toolName)
	}
}
