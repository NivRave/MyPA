package llm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/require"
)

func TestLLMFunctionCalling(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set. Skipping real API test.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewClient(ctx, apiKey, "gemini-2.5-flash")
	require.NoError(t, err)

	systemPrompt := "You are a helpful personal assistant. Your current time is " + time.Now().Format(time.RFC3339) + ". The user's timezone is America/New_York."
	history := []models.ChatMessage{}
	newMessage := "Block 2 hours for deep work tomorrow afternoon starting at 1pm."

	resp, err := client.Chat(ctx, systemPrompt, history, newMessage, nil, "")
	require.NoError(t, err)

	// We expect the model to invoke the tool instead of just returning text
	require.NotNil(t, resp.ToolCall, "Expected LLM to return a tool call, but got text: %s", resp.Text)

	require.Equal(t, "create_calendar_event", resp.ToolCall.Name)

	args := resp.ToolCall.Args
	require.NotNil(t, args)
	
	// Check that the required arguments are present
	require.Contains(t, args, "title")
	require.Contains(t, args, "start_time")
	require.Contains(t, args, "end_time")
	require.Contains(t, args, "timezone")

	// Print the extracted arguments for visibility
	t.Logf("Function Called: %s", resp.ToolCall.Name)
	for k, v := range args {
		t.Logf("  %s: %v", k, v)
	}
}
