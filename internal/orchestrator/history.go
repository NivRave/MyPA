package orchestrator

import "github.com/nivik/mypa/internal/models"

// sanitizeHistory makes stored chat history safe to send to Gemini (P2-9). History is capped
// with LTRIM, which can cut anywhere — including between a model tool-call message and its
// function-response message. Gemini rejects such histories, so:
//   - an assistant message with ToolCalls is kept only if the very next message is a function
//     message answering every call (and that function message is kept with it);
//   - function messages are kept only as part of such a pair;
//   - leading messages are dropped until the first plain user message.
func sanitizeHistory(h []models.ChatMessage) []models.ChatMessage {
	out := make([]models.ChatMessage, 0, len(h))
	for i := 0; i < len(h); i++ {
		m := h[i]
		switch {
		case len(m.ToolCalls) > 0:
			if i+1 < len(h) && h[i+1].Role == "function" && len(h[i+1].ToolResponses) == len(m.ToolCalls) {
				out = append(out, m, h[i+1])
				i++ // pair consumed
			}
			// else: orphaned call → drop
		case m.Role == "function":
			// a response not directly preceded by its call (handled above) → drop
		default:
			out = append(out, m)
		}
	}
	for len(out) > 0 && (out[0].Role != "user" || len(out[0].ToolResponses) > 0) {
		if len(out[0].ToolCalls) > 0 {
			out = out[2:] // drop the whole leading call/response pair
			continue
		}
		out = out[1:]
	}
	return out
}
