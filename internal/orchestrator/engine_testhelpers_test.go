package orchestrator

import (
	"context"
	"strings"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/models"
	"google.golang.org/genai"
)

// handleToolCall is a legacy single-tool entry point kept only for tests (moved out of production
// code in P4-1). Production turns use handleToolCalls via runTurn.
func (e *Engine) handleToolCall(ctx context.Context, msg channel.Inbound, history []models.ChatMessage, systemPrompt string, toolCall *genai.FunctionCall) (string, error) {
	if toolCall.Name == "schedule_reminder" || toolCall.Name == "unknown_tool" || toolCall.Name == "fetch_webpage" {
		return e.executeSingleTool(ctx, msg, history, systemPrompt, toolCall), nil
	}
	if toolCall.Name == "search_emails" {
		res := e.executeSingleTool(ctx, msg, history, systemPrompt, toolCall)
		// Output may be wrapped as untrusted content (P3-1), so match on content, not equality.
		// NOTE: test-only legacy helper; with non-empty results it falls through and executes the
		// tool a second time via handleToolCalls — slated for removal in P4-1.
		if strings.Contains(res, "You have no unread emails.") {
			return res, nil
		}
	}
	return e.handleToolCalls(ctx, msg, history, systemPrompt, []*genai.FunctionCall{toolCall}, 1)
}
