package llm

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nivik/mypa/internal/models"
	"google.golang.org/genai"
)

// Client wraps the Gemini API client.
type Client struct {
	genaiClient *genai.Client
	model       string
}

// NewClient initializes a new Gemini client.
func NewClient(ctx context.Context, apiKey, model string) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("gemini api key is required")
	}
	if model == "" {
		model = "gemini-2.5-flash"
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}

	return &Client{
		genaiClient: client,
		model:       model,
	}, nil
}

// Response represents the output from the LLM, which could be either a text reply or a tool call.
type Response struct {
	Text        string
	ToolCall    *genai.FunctionCall
	IsError     bool
}

// Chat sends the conversation history and the new message to the LLM.
// It provides the LLM with the CalendarEventTool so it can request actions.
func (c *Client) Chat(ctx context.Context, systemInstruction string, history []models.ChatMessage, newMessage string) (*Response, error) {
	// Build the session context
	contents := []*genai.Content{}

	for _, msg := range history {
		// Map our internal role ("user" or "assistant") to Gemini roles ("user" or "model")
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, &genai.Content{
			Role:  role,
			Parts: []*genai.Part{genai.NewPartFromText(msg.Content)},
		})
	}

	// Add the new message
	contents = append(contents, &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{genai.NewPartFromText(newMessage)},
	})

	// Configure the model
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(systemInstruction)},
		},
		Tools:       []*genai.Tool{CalendarEventTool},
		Temperature: genai.Ptr[float32](0.2), // Low temp for more deterministic tool calling
	}

	resp, err := c.genaiClient.Models.GenerateContent(ctx, c.model, contents, config)
	if err != nil {
		slog.Error("gemini api error", "error", err)
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates returned from model")
	}

	part := resp.Candidates[0].Content.Parts[0]

	// Check if the model decided to call a function
	if part.FunctionCall != nil {
		slog.Info("LLM invoked function", "name", part.FunctionCall.Name)
		return &Response{
			ToolCall: part.FunctionCall,
		}, nil
	}

	// Otherwise, it's a text response
	return &Response{
		Text: part.Text,
	}, nil
}

// GenerateEmbedding generates a vector embedding for the given text.
func (c *Client) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	// Use gemini-embedding-001 model
	contents := []*genai.Content{
		{Parts: []*genai.Part{genai.NewPartFromText(text)}},
	}
	resp, err := c.genaiClient.Models.EmbedContent(ctx, "gemini-embedding-001", contents, nil)
	
	if err != nil {
		return nil, fmt.Errorf("failed to generate embedding: %w", err)
	}

	if len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return resp.Embeddings[0].Values, nil
}
