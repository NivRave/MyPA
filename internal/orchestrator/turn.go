package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/state"
	"github.com/pgvector/pgvector-go"
	"golang.org/x/sync/errgroup"
	"google.golang.org/genai"
)

// runTurn runs the LLM part of a turn (P4-1): history, tool selection, memory retrieval, prompt
// assembly, the model call (with the Hebrew content-block retry), the tool loop, the reply and
// history persistence. It returns the final response text (recorded in the audit session).
func (e *Engine) runTurn(ctx context.Context, user *models.User, msg models.Message, media mediaAttachment, effects *turnEffects) (response string, err error) {
	// 2. Fetch conversation history
	history, err := e.store.GetChatHistory(ctx, msg.UserID)
	if err != nil {
		return response, fmt.Errorf("failed to get chat history: %w", err)
	}
	// LTRIM may have split a tool call from its response; Gemini rejects that (P2-9).
	history = sanitizeHistory(history)

	// 3. Select this turn's tools first: the prompt's tool guidance follows them (P2-6).
	// Dynamic Two-Tier Tool Context Selection (SCALE-01) + sticky follow-up domains (P2-2)
	var matchedDomains []DomainGroup
	var activeToolNames map[string]bool // nil = no registry → all guidance
	if e.toolRegistry != nil {
		if e.intentMatcher != nil {
			matchedDomains = e.intentMatcher.MatchDomains(msg.Text)
		}
		selected := unionDomains(matchedDomains, e.loadStickyDomains(ctx, msg.UserID))
		activeTools := e.toolRegistry.SelectDeclarations(selected, false)
		ctx = llm.WithActiveTools(ctx, activeTools)
		activeToolNames = make(map[string]bool, len(activeTools))
		for _, d := range activeTools {
			activeToolNames[d.Name] = true
		}
	}

	// Search long-term semantic memory — skipped for trivial acknowledgements (P2-5): an
	// embedding of "thanks!" retrieves noise and costs an API call per message.
	var memoryLines []string
	var embedding []float32
	if !isTrivialMessage(msg.Text) {
		embedding, err = e.llm.GenerateEmbedding(ctx, msg.Text)
	}
	if err == nil && len(embedding) > 0 {
		// P2-4: single memory store. Visibility (own personal + family-shared) is enforced by
		// the repository's tenant filter, so no per-member user ID list is needed.
		memories, searchErr := e.db.SearchSimilarMemories(ctx, msg.UserID, user.FamilyGroup, pgvector.NewVector(embedding), 5)
		if searchErr != nil {
			slog.Warn("memory retrieval failed; continuing without memories", "user_id", msg.UserID, "error", searchErr)
		}
		for _, m := range memories {
			scopeTag := ""
			if m.Scope == "family" {
				scopeTag = " (Family Shared)"
			}
			memoryLines = append(memoryLines, "- "+m.Fact+scopeTag)
		}
	}

	// Dynamic per-module system prompt sections (cached per user, P1-3)
	moduleSections := ""
	if e.toolRegistry != nil {
		callCtx := e.buildCallContext(ctx, user, msg)
		moduleSections = e.toolRegistry.CombinedSystemPrompt(ctx, callCtx)
	}

	// Build the system prompt, most-stable sections first (P2-6, see prompt.go)
	systemPrompt := buildSystemPrompt(promptInputs{
		UserName:       user.Name,
		Role:           user.Role,
		FamilyGroup:    user.FamilyGroup,
		ActiveTools:    activeToolNames,
		ModuleSections: moduleSections,
		Memories:       memoryLines,
		Now:            time.Now(),
		Location:       e.location(),
		Calendar:       e.calendar,
	})

	// 4. Call LLM
	promptText := msg.Text
	resp, err := e.llm.Chat(ctx, systemPrompt, history, promptText, media.Data, media.MimeType)
	if err != nil {
		var blockedErr *llm.ContentBlockedError
		if errors.As(err, &blockedErr) {
			normalized := NormalizeHebrewPrepositionNumbers(promptText)
			if normalized != promptText {
				slog.Info("retrying LLM chat with normalized prompt after content block", "message_id", msg.ID, "text_len", len([]rune(promptText)), "reason", blockedErr.BlockReason) // P3-3: no content
				resp, err = e.llm.Chat(ctx, systemPrompt, history, normalized, media.Data, media.MimeType)
			}
		}
	}
	if err != nil {
		var blockedErr *llm.ContentBlockedError
		if errors.As(err, &blockedErr) {
			slog.Warn("llm prompt blocked by safety filter", "user_id", msg.UserID, "reason", blockedErr.BlockReason)
			replyText := "⚠️ The AI safety filter flagged this request (likely a false positive). Please try phrasing it slightly differently, for example by adding a hyphen (e.g. 'ב-18:00') or using full time/date words."
			response = replyText
			_ = e.sendMessage(ctx, msg, replyText)
			return response, nil
		}
		return response, fmt.Errorf("llm chat failed: %w", err)
	}

	// 5. Handle LLM Response
	var replyText string

	if len(resp.ToolCalls) > 0 {
		// Generate AuditEvent for tool call?
		// We can add this later, for now just skip assigning actionTaken

		// Initialize the loop history with the user's message
		loopHistory := append(history, models.ChatMessage{
			Role:          "user",
			Content:       msg.Text,
			PhotoData:     media.Data,
			PhotoMimeType: media.MimeType,
		})

		replyText, _ = e.handleToolCalls(ctx, msg, loopHistory, systemPrompt, resp.ToolCalls, 1)
	} else {
		// No tool call
		replyText = resp.Text
	}

	// Remember what this turn used so a short follow-up ("yes, the second one") keeps its tools.
	e.saveStickyDomains(ctx, msg.UserID, matchedDomains, effects.calledTools())

	// 6. Send reply to Telegram
	if strings.TrimSpace(replyText) == "" {
		replyText = "⚠️ Sorry, I didn't get a response from the AI. Please try again."
	}
	response = replyText

	if err := e.sendMessage(ctx, msg, replyText); err != nil {
		return response, fmt.Errorf("failed to send reply message: %w", err)
	}
	markSideEffect(ctx) // reply delivered: a later failure must not re-send it

	// 7. Update conversation history (SCALE-02: strip PhotoData bytes)
	persistedContent := msg.Text
	if len(media.Data) > 0 {
		marker := formatAttachmentMarker(media.MimeType, len(media.Data))
		if strings.TrimSpace(persistedContent) == "" {
			persistedContent = marker
		} else {
			persistedContent = fmt.Sprintf("%s\n%s", strings.TrimSpace(persistedContent), marker)
		}
	}

	_ = e.store.AppendChatHistory(ctx, msg.UserID, models.ChatMessage{
		Role:    "user",
		Content: persistedContent,
	})
	// P2-9: keep the turn's tool calls and (truncated) results, so a follow-up such as
	// "delete that event" can refer to IDs created in this turn.
	for _, m := range effects.historyMessages() {
		_ = e.store.AppendChatHistory(ctx, msg.UserID, m)
	}
	_ = e.store.AppendChatHistory(ctx, msg.UserID, models.ChatMessage{Role: "assistant", Content: replyText})

	return response, nil
}

// MaxToolRecursionDepth is the maximum number of recursive tool loops allowed per turn.
const MaxToolRecursionDepth = 8

// MaxConcurrentTools is the maximum number of tool calls executed in parallel within a single turn.
const MaxConcurrentTools = 8

const maxToolIterations = MaxToolRecursionDepth

// executeToolsConcurrently executes a batch of function calls in parallel using errgroup.
// It guarantees deterministic indexing, context cancellation, panic recovery, and bounded concurrency.
func (e *Engine) executeToolsConcurrently(
	ctx context.Context,
	msg models.Message,
	history []models.ChatMessage,
	systemPrompt string,
	toolCalls []*genai.FunctionCall,
) ([]models.FunctionCall, []models.FunctionResponse) {
	n := len(toolCalls)
	if n == 0 {
		return nil, nil
	}

	funcCalls := make([]models.FunctionCall, n)
	funcResponses := make([]models.FunctionResponse, n)

	for i, tc := range toolCalls {
		funcCalls[i] = models.FunctionCall{Name: tc.Name, Args: tc.Args}
	}

	if n == 1 {
		tc := toolCalls[0]
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic recovered during single tool execution",
						"tool", tc.Name,
						"panic", r,
						"user_id", msg.UserID,
					)
					funcResponses[0] = models.FunctionResponse{
						Name: tc.Name,
						Response: map[string]interface{}{
							"result": fmt.Sprintf("❌ Internal panic during tool execution: %v", r),
						},
					}
				}
			}()
			resultStr := e.executeSingleTool(ctx, msg, history, systemPrompt, tc)
			funcResponses[0] = models.FunctionResponse{
				Name:     tc.Name,
				Response: map[string]interface{}{"result": resultStr},
			}
		}()
		return funcCalls, funcResponses
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(MaxConcurrentTools)

	for i, tc := range toolCalls {
		idx := i
		call := tc
		g.Go(func() error {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic recovered during concurrent tool execution",
						"tool", call.Name,
						"panic", r,
						"user_id", msg.UserID,
					)
					funcResponses[idx] = models.FunctionResponse{
						Name: call.Name,
						Response: map[string]interface{}{
							"result": fmt.Sprintf("❌ Internal panic during tool execution: %v", r),
						},
					}
				}
			}()

			resultStr := e.executeSingleTool(gctx, msg, history, systemPrompt, call)
			funcResponses[idx] = models.FunctionResponse{
				Name:     call.Name,
				Response: map[string]interface{}{"result": resultStr},
			}
			return nil
		})
	}
	_ = g.Wait()

	return funcCalls, funcResponses
}

func (e *Engine) handleToolCalls(
	ctx context.Context,
	msg models.Message,
	history []models.ChatMessage,
	systemPrompt string,
	toolCalls []*genai.FunctionCall,
	iteration int,
) (string, error) {
	if iteration <= 0 {
		iteration = 1
	}

	funcCalls, funcResponses := e.executeToolsConcurrently(ctx, msg, history, systemPrompt, toolCalls)

	// Add the actual assistant tool calls natively
	extendedHistory := append(history, models.ChatMessage{
		Role:      "assistant",
		ToolCalls: funcCalls,
	})

	// Add the function responses natively
	extendedHistory = append(extendedHistory, models.ChatMessage{
		Role:          "function",
		ToolResponses: funcResponses,
	})

	// Check iteration limit (SCALE-03: bound to 8 iterations)
	if iteration >= maxToolIterations {
		slog.Warn("max tool recursion reached, requesting terminal summary without tools",
			"user_id", msg.UserID, "iteration", iteration)

		terminalPrompt := "Maximum tool execution limit reached (8 steps). Do NOT call any more tools. " +
			"Please provide a friendly, clear, and comprehensive final summary of all actions completed so far directly to the user."

		// Force no-tools call to guarantee conversational termination
		summaryResp, err := e.llm.Chat(llm.WithNoTools(ctx), systemPrompt, extendedHistory, terminalPrompt, nil, "")
		if err != nil {
			return "✅ Actions were executed (maximum 8 steps reached).", nil
		}
		if summaryResp != nil && summaryResp.Text != "" {
			return summaryResp.Text, nil
		}
		return "✅ All requested actions have been completed up to the maximum iteration limit.", nil
	}

	// Normal continuation prompt
	prompt := "Please continue fulfilling the user's request. What's next? If you are completely done, provide a friendly, nicely formatted final summary of all your actions directly to the user."

	summaryResp, err := e.llm.Chat(ctx, systemPrompt, extendedHistory, prompt, nil, "")
	if err != nil {
		var blockedErr *llm.ContentBlockedError
		if errors.As(err, &blockedErr) {
			return "⚠️ Actions were executed, but the final response summary was flagged by the AI content filter.", nil
		}
		return "❌ Failed to process: " + err.Error(), nil
	}

	if summaryResp != nil && len(summaryResp.ToolCalls) > 0 {
		extendedHistory = append(extendedHistory, models.ChatMessage{Role: "user", Content: prompt})
		return e.handleToolCalls(ctx, msg, extendedHistory, systemPrompt, summaryResp.ToolCalls, iteration+1)
	}

	if summaryResp != nil {
		return summaryResp.Text, nil
	}
	return "", nil
}


func (e *Engine) executeSingleTool(ctx context.Context, msg models.Message, history []models.ChatMessage, systemPrompt string, toolCall *genai.FunctionCall) string {
	slog.Info("llm requested tool call", "tool", toolCall.Name)

	// P3-1: one list of sensitive tools (security.go) instead of four hard-coded meta tools.
	if RequiresConfirmationWhenUntrusted(toolCall.Name) {
		secState := GetTurnSecurityState(ctx)
		if secState != nil {
			isUntrusted, sources := secState.IsUntrusted()
			if isUntrusted {
				if e.store != nil {
					stageData := state.PendingMetaToolAction{
						ToolName:  toolCall.Name,
						Args:      toolCall.Args,
						UserID:    msg.UserID,
						CreatedAt: time.Now(),
					}
					_ = e.store.StagePendingMetaTool(ctx, msg.UserID, stageData, 5*time.Minute)
				}

				sourceDesc := "external untrusted content"
				if len(sources) > 0 {
					sourceDesc = strings.Join(sources, ", ")
				}

				return fmt.Sprintf(
					"🔒 **Interactive Security Confirmation Required**\n\n"+
						"The assistant tried to perform a sensitive action (`%s`) after reading external data (%s).\n"+
						"To prevent indirect prompt injection, this modification was NOT applied automatically.\n\n"+
						"👉 If you intended to make this change, please reply with:\n`CONFIRM %s`",
					toolCall.Name, sourceDesc, strings.ToUpper(toolCall.Name),
				)
			}
		}
	}

	// Delegate to modular ToolRegistry if registered
	if e.toolRegistry != nil {
		// P2-7: reuse the user resolved at the start of the turn; look it up only for callers
		// outside a turn (direct invocations, tests).
		user := turnUser(ctx)
		if user == nil && e.db != nil {
			user, _ = e.db.GetUser(msg.UserID)
		}
		callCtx := e.buildCallContext(ctx, user, msg)
		res, handled, err := e.toolRegistry.Execute(ctx, callCtx, toolCall.Name, toolCall.Args)
		if handled {
			out := res
			if err != nil {
				slog.Error("registry tool execution failed", "tool", toolCall.Name, "error", err)
				out = fmt.Sprintf("❌ Error executing %s: %v", toolCall.Name, err)
			}
			// P3-1: output of external-data tools is attacker-controllable. Mark the turn untrusted
			// (gating sensitive tools for the rest of it) and hand the result to the model wrapped.
			if source, ok := untrustedOutputTools[toolCall.Name]; ok && err == nil {
				GetTurnSecurityState(ctx).MarkUntrusted(source + ":" + toolCall.Name)
				if !strings.Contains(out, "<untrusted_external_content") {
					out = WrapUntrustedContent(source, toolCall.Name, out)
				}
			}
			recordToolCall(ctx, toolCall.Name, toolCall.Args, out)
			// The tool ran (possibly partially, even on error): retrying the turn would duplicate it.
			// Read-only built-ins computed locally are exempt.
			if !readOnlyBuiltinTools[toolCall.Name] {
				markSideEffect(ctx)
			}
			return out
		}
	}

	if toolCall.Name == "get_current_time" {
		loc := time.UTC
		if strings.TrimSpace(e.timezone) != "" {
			if l, err := time.LoadLocation(strings.TrimSpace(e.timezone)); err == nil {
				loc = l
			}
		}
		if toolCall.Args != nil {
			if tzVal, ok := toolCall.Args["timezone"].(string); ok && strings.TrimSpace(tzVal) != "" {
				if l, err := time.LoadLocation(strings.TrimSpace(tzVal)); err == nil {
					loc = l
				}
			}
		}
		now := time.Now().In(loc)
		resp := map[string]interface{}{
			"current_time": now.Format(time.RFC3339),
			"timezone":     loc.String(),
			"day_of_week":  now.Weekday().String(),
			"unix":         now.Unix(),
		}
		b, _ := json.Marshal(resp)
		return string(b)
	}

	return "⚠️ LLM tried to call an unknown tool."
}
