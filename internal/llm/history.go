package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nivik/mypa/internal/models"
	"google.golang.org/genai"
)

// turn is one message of a request in the form the HTTP adapters build from: a role ("user" or
// "assistant") and its parts in order.
type turn struct {
	Role  string
	Parts []part
}

// part is one piece of a turn: text, an attachment, a tool call or a tool result.
type part struct {
	Text string
	// Data and MimeType are an attachment (an image or a PDF).
	Data     []byte
	MimeType string
	// Call is a tool call the model made, with the ID that pairs it with its result.
	Call   *models.FunctionCall
	CallID string
	// Result is a tool result, answering the call with ResultID.
	Result   *models.FunctionResponse
	ResultID string
}

// buildTurns converts a request's history and new message into turns. The stored history has no
// tool call IDs, which Anthropic and OpenAI need to pair a call with its result, so each call gets
// one here ("call_1", "call_2", ...) and the result in the same position of the "function" message
// that follows gets the same one. A call without that message, or a result without its call, is
// left out: both APIs refuse an unpaired call or result (orchestrator sanitizeHistory already
// drops them; this keeps a request valid whatever it is given). Consecutive messages of one role
// are merged, because Anthropic needs the roles to alternate and a tool result is a user message
// there.
func buildTurns(req Request) []turn {
	var turns []turn
	add := func(role string, parts []part) {
		if len(parts) == 0 {
			return
		}
		if n := len(turns); n > 0 && turns[n-1].Role == role {
			turns[n-1].Parts = append(turns[n-1].Parts, parts...)
			return
		}
		turns = append(turns, turn{Role: role, Parts: parts})
	}
	nextID := 0
	h := req.History
	for i := 0; i < len(h); i++ {
		m := h[i]
		switch {
		case len(m.ToolCalls) > 0:
			if i+1 >= len(h) || h[i+1].Role != "function" || len(h[i+1].ToolResponses) != len(m.ToolCalls) {
				continue // an unpaired call
			}
			var calls, results []part
			if m.Content != "" {
				calls = append(calls, part{Text: m.Content})
			}
			for j := range m.ToolCalls {
				nextID++
				id := fmt.Sprintf("call_%d", nextID)
				calls = append(calls, part{Call: &m.ToolCalls[j], CallID: id})
				results = append(results, part{Result: &h[i+1].ToolResponses[j], ResultID: id})
			}
			add("assistant", calls)
			add("user", results)
			i++
		case m.Role == "function":
			continue // a result without its call
		default:
			role := "user"
			if m.Role == "assistant" || m.Role == "model" {
				role = "assistant"
			}
			add(role, contentParts(m.Content, m.PhotoData, m.PhotoMimeType))
		}
	}
	parts := contentParts(req.Message, req.PhotoData, req.PhotoMimeType)
	if len(parts) == 0 {
		parts = []part{{Text: " "}} // a request must end with a user message that is not empty
	}
	add("user", parts)
	return turns
}

func contentParts(text string, data []byte, mime string) []part {
	var parts []part
	if text != "" {
		parts = append(parts, part{Text: text})
	}
	if len(data) > 0 && mime != "" {
		parts = append(parts, part{Data: data, MimeType: mime})
	}
	return parts
}

// resultJSON is a tool result as the JSON text both HTTP APIs carry in a tool result.
func resultJSON(r *models.FunctionResponse) string {
	b, err := json.Marshal(r.Response)
	if err != nil || r.Response == nil {
		return "{}"
	}
	return string(b)
}

// jsonSchema converts a genai schema, which spells types in upper case (OBJECT), into the JSON
// Schema Anthropic and OpenAI take (object).
func jsonSchema(s *genai.Schema) map[string]any {
	if s == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	out := map[string]any{}
	if s.Type != "" && s.Type != genai.TypeUnspecified {
		out["type"] = strings.ToLower(string(s.Type))
	}
	if s.Description != "" {
		out["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		out["enum"] = s.Enum
	}
	if s.Format != "" {
		out["format"] = s.Format
	}
	if s.Items != nil {
		out["items"] = jsonSchema(s.Items)
	}
	if len(s.AnyOf) > 0 {
		var anyOf []map[string]any
		for _, a := range s.AnyOf {
			anyOf = append(anyOf, jsonSchema(a))
		}
		out["anyOf"] = anyOf
	}
	if s.Type == genai.TypeObject || len(s.Properties) > 0 {
		props := map[string]any{}
		for name, p := range s.Properties {
			props[name] = jsonSchema(p)
		}
		out["properties"] = props
	}
	if len(s.Required) > 0 {
		out["required"] = s.Required
	}
	if s.Minimum != nil {
		out["minimum"] = *s.Minimum
	}
	if s.Maximum != nil {
		out["maximum"] = *s.Maximum
	}
	if s.MinItems != nil {
		out["minItems"] = *s.MinItems
	}
	if s.MaxItems != nil {
		out["maxItems"] = *s.MaxItems
	}
	if s.Pattern != "" {
		out["pattern"] = s.Pattern
	}
	if s.Default != nil {
		out["default"] = s.Default
	}
	return out
}

// toolSchema is a declaration's parameters as a JSON Schema object, which both APIs require even
// for a tool without parameters.
func toolSchema(d *genai.FunctionDeclaration) map[string]any {
	if d.Parameters == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	s := jsonSchema(d.Parameters)
	s["type"] = "object"
	if _, ok := s["properties"]; !ok {
		s["properties"] = map[string]any{}
	}
	return s
}
