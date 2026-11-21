// Package conformance is the provider conformance suite (V4-02): the same scenarios (Hebrew, tool
// calls, tool results, images, PDFs, untrusted content, no tools, no sampling parameters, safety
// blocks, unavailability) run against every chat model provider. In tests and in the docs
// generator they run against fakes of the three wire formats, which check each request the way
// the real API does and answer with a canned reply; `go run ./cmd/devtools/llmconformance` runs
// the scenarios that need no fake against the real providers. The capability matrix in SYSTEM.md
// is generated from the declarations (llm.CapabilitiesOf) whose scenarios pass here.
package conformance

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/llm"
)

// The hosts the fakes answer for. OllamaURL is the OLLAMA_BASE_URL the suite configures.
const (
	geminiHost = "generativelanguage.googleapis.com"
	OllamaURL  = "http://ollama.test:11434"
)

// Reply is what a fake answers with.
type Reply struct {
	// Text is a text reply.
	Text string
	// Call is a tool call reply.
	Call *Call
	// Blocked answers with the provider's safety block.
	Blocked bool
	// Status answers with this HTTP error status.
	Status int
	// Down makes the provider unreachable: the request fails before any response.
	Down bool
	// InputTokens and OutputTokens are the token counts the reply reports, in the provider's own
	// usage fields (V4-04).
	InputTokens, OutputTokens int
}

// Call is a tool call in a reply.
type Call struct {
	Name string
	Args map[string]any
}

// Seen is a request as a fake received it, in one form whatever the wire format.
type Seen struct {
	Model    string
	System   string
	Messages []SeenMessage
	// Tools are the declared tool names, and Schemas their parameter schemas as sent.
	Tools   []string
	Schemas map[string]map[string]any
	// Sampling lists the sampling parameters the request carried (BL-29).
	Sampling []string
	// Auth is the credential the request carried ("" when none).
	Auth string
}

// SeenMessage is one message of a request. Role is "user" or "assistant".
type SeenMessage struct {
	Role        string
	Texts       []string
	Attachments []Attachment
	Calls       []SeenCall
	Results     []SeenResult
}

// Attachment is an attachment as sent.
type Attachment struct {
	MimeType string
	Data     []byte
}

// SeenCall is a tool call in the history. ID is empty for Gemini, which pairs by name.
type SeenCall struct {
	ID, Name string
	Args     map[string]any
}

// SeenResult is a tool result in the history.
type SeenResult struct {
	ID, Name string
	Content  string
}

// AllTexts returns every text of every message, in order.
func (s Seen) AllTexts() []string {
	var out []string
	for _, m := range s.Messages {
		out = append(out, m.Texts...)
	}
	return out
}

// Last returns the last message.
func (s Seen) Last() SeenMessage {
	if len(s.Messages) == 0 {
		return SeenMessage{}
	}
	return s.Messages[len(s.Messages)-1]
}

// Fake is one provider's fake. It is safe for concurrent use.
type Fake struct {
	provider string
	wire     string

	mu    sync.Mutex
	reply Reply
	seen  []Seen
}

// Answer sets what the fake answers with from now on.
func (f *Fake) Answer(r Reply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply = r
}

// Requests returns the requests the fake received, oldest first.
func (f *Fake) Requests() []Seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Seen(nil), f.seen...)
}

// LastRequest returns the newest request; ok is false when there is none.
func (f *Fake) LastRequest() (Seen, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) == 0 {
		return Seen{}, false
	}
	return f.seen[len(f.seen)-1], true
}

// Reset forgets the requests and answers "ok" again.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = nil
	f.reply = Reply{Text: "ok"}
}

func (f *Fake) current() Reply {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reply
}

func (f *Fake) record(s Seen) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, s)
}

// Fakes is one fake per provider behind one HTTP client.
type Fakes struct {
	byProvider map[string]*Fake
	byHost     map[string]*Fake
}

// NewFakes returns a fake for every provider, each answering "ok".
func NewFakes() *Fakes {
	fs := &Fakes{byProvider: map[string]*Fake{}, byHost: map[string]*Fake{}}
	ollama, _ := url.Parse(OllamaURL)
	hosts := map[string]string{
		config.ProviderGemini:    geminiHost,
		config.ProviderAnthropic: hostOf(llm.AnthropicURL),
		config.ProviderOpenAI:    hostOf(llm.OpenAIURL),
		config.ProviderGrok:      hostOf(llm.XAIURL),
		config.ProviderOllama:    ollama.Host,
	}
	for _, name := range config.Providers() {
		f := &Fake{provider: name, wire: llm.CapabilitiesOf(name).Wire, reply: Reply{Text: "ok"}}
		fs.byProvider[name] = f
		fs.byHost[hosts[name]] = f
	}
	return fs
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u.Host
}

// Of returns the fake of a provider.
func (fs *Fakes) Of(provider string) *Fake { return fs.byProvider[provider] }

// Client is the HTTP client that reaches the fakes at the providers' real addresses
// (llm.WithHTTPClient). A request to any other host fails.
func (fs *Fakes) Client() *http.Client { return &http.Client{Transport: fs} }

// ErrDown is the transport error of a provider whose fake is down.
var ErrDown = errors.New("connection refused (fake provider is down)")

// RoundTrip serves a request from the fake of its host.
func (fs *Fakes) RoundTrip(r *http.Request) (*http.Response, error) {
	f, ok := fs.byHost[r.URL.Host]
	if !ok {
		return nil, fmt.Errorf("no fake provider at %s", r.URL.Host)
	}
	if f.current().Down {
		return nil, ErrDown
	}
	rec := httptest.NewRecorder()
	f.serve(rec, r)
	return rec.Result(), nil
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, f.wire, http.StatusBadRequest, "unreadable body")
		return
	}
	var seen Seen
	switch f.wire {
	case llm.WireGemini:
		seen, err = parseGemini(r, body)
	case llm.WireAnthropic:
		seen, err = parseAnthropic(r, body)
	default:
		seen, err = parseOpenAI(r, body, f.provider != config.ProviderOllama)
	}
	if err != nil {
		// What the real API does with a malformed request: refuse it.
		writeError(w, f.wire, http.StatusBadRequest, err.Error())
		return
	}
	f.record(seen)
	reply := f.current()
	if reply.Status != 0 {
		writeError(w, f.wire, reply.Status, "fake error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch f.wire {
	case llm.WireGemini:
		_, _ = w.Write(geminiReply(reply))
	case llm.WireAnthropic:
		_, _ = w.Write(anthropicReply(reply))
	default:
		_, _ = w.Write(openAIReply(reply))
	}
}

func writeError(w http.ResponseWriter, wire string, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var body any
	switch wire {
	case llm.WireGemini:
		body = map[string]any{"error": map[string]any{"code": status, "message": msg, "status": http.StatusText(status)}}
	case llm.WireAnthropic:
		body = map[string]any{"type": "error", "error": map[string]any{"type": "fake_error", "message": msg}}
	default:
		body = map[string]any{"error": map[string]any{"message": msg, "type": "fake_error"}}
	}
	_ = json.NewEncoder(w).Encode(body)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// samplingKeys are the sampling parameters any of the APIs know.
var samplingKeys = []string{"temperature", "top_p", "top_k", "topP", "topK", "frequency_penalty", "presence_penalty", "frequencyPenalty", "presencePenalty", "seed"}

func samplingIn(m map[string]any) []string {
	var out []string
	for _, k := range samplingKeys {
		if _, ok := m[k]; ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- Gemini

func parseGemini(r *http.Request, body []byte) (Seen, error) {
	var req struct {
		Contents []struct {
			Role  string           `json:"role"`
			Parts []map[string]any `json:"parts"`
		} `json:"contents"`
		SystemInstruction *struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"systemInstruction"`
		Tools []struct {
			FunctionDeclarations []struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"functionDeclarations"`
		} `json:"tools"`
		GenerationConfig map[string]any `json:"generationConfig"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return Seen{}, err
	}
	seen := Seen{Schemas: map[string]map[string]any{}, Auth: r.Header.Get("x-goog-api-key")}
	if i := strings.Index(r.URL.Path, "models/"); i >= 0 {
		seen.Model = strings.TrimSuffix(r.URL.Path[i+len("models/"):], ":generateContent")
	}
	if !strings.HasSuffix(r.URL.Path, ":generateContent") {
		return Seen{}, fmt.Errorf("unexpected path %s", r.URL.Path)
	}
	if req.SystemInstruction != nil {
		for _, p := range req.SystemInstruction.Parts {
			seen.System += p.Text
		}
	}
	for _, t := range req.Tools {
		for _, d := range t.FunctionDeclarations {
			seen.Tools = append(seen.Tools, d.Name)
			seen.Schemas[d.Name] = d.Parameters
		}
	}
	seen.Sampling = samplingIn(req.GenerationConfig)
	if len(req.Contents) == 0 {
		return Seen{}, errors.New("contents is empty")
	}
	for _, c := range req.Contents {
		m := SeenMessage{Role: "user"}
		if c.Role == "model" {
			m.Role = "assistant"
		} else if c.Role != "user" {
			return Seen{}, fmt.Errorf("unknown role %q", c.Role)
		}
		for _, p := range c.Parts {
			switch {
			case p["text"] != nil:
				m.Texts = append(m.Texts, p["text"].(string))
			case p["inlineData"] != nil:
				d := p["inlineData"].(map[string]any)
				data, err := base64.StdEncoding.DecodeString(d["data"].(string))
				if err != nil {
					return Seen{}, err
				}
				m.Attachments = append(m.Attachments, Attachment{MimeType: d["mimeType"].(string), Data: data})
			case p["functionCall"] != nil:
				fc := p["functionCall"].(map[string]any)
				args, _ := fc["args"].(map[string]any)
				m.Calls = append(m.Calls, SeenCall{Name: fc["name"].(string), Args: args})
			case p["functionResponse"] != nil:
				fr := p["functionResponse"].(map[string]any)
				m.Results = append(m.Results, SeenResult{Name: fr["name"].(string), Content: string(mustJSON(fr["response"]))})
			}
		}
		seen.Messages = append(seen.Messages, m)
	}
	return seen, nil
}

func geminiReply(r Reply) []byte {
	// Gemini reports the prompt and the reply apart, and a thinking model's thoughts apart again;
	// the fake puts one output token in thoughts when it has more than one, so the adapter must
	// add them.
	thoughts := 0
	if r.OutputTokens > 1 {
		thoughts = 1
	}
	usage := map[string]any{"promptTokenCount": r.InputTokens, "candidatesTokenCount": r.OutputTokens - thoughts,
		"thoughtsTokenCount": thoughts, "totalTokenCount": r.InputTokens + r.OutputTokens}
	switch {
	case r.Blocked:
		return mustJSON(map[string]any{"promptFeedback": map[string]any{"blockReason": "SAFETY"}, "candidates": []any{}})
	case r.Call != nil:
		return mustJSON(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model",
			"parts": []any{map[string]any{"functionCall": map[string]any{"name": r.Call.Name, "args": r.Call.Args}}}}, "finishReason": "STOP"}},
			"usageMetadata": usage})
	}
	return mustJSON(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model",
		"parts": []any{map[string]any{"text": r.Text}}}, "finishReason": "STOP"}}, "usageMetadata": usage})
}

// ---------------------------------------------------------------- Anthropic

var anthropicImageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

func parseAnthropic(r *http.Request, body []byte) (Seen, error) {
	if r.URL.Path != "/v1/messages" {
		return Seen{}, fmt.Errorf("unexpected path %s", r.URL.Path)
	}
	if r.Header.Get("anthropic-version") == "" {
		return Seen{}, errors.New("anthropic-version header is required")
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return Seen{}, err
	}
	if _, ok := req["max_tokens"].(float64); !ok {
		return Seen{}, errors.New("max_tokens is required")
	}
	seen := Seen{Schemas: map[string]map[string]any{}, Auth: r.Header.Get("x-api-key"), Sampling: samplingIn(req)}
	seen.Model, _ = req["model"].(string)
	if sys, ok := req["system"].(string); ok {
		seen.System = sys
	}
	tools, _ := req["tools"].([]any)
	for _, t := range tools {
		tm := t.(map[string]any)
		schema, _ := tm["input_schema"].(map[string]any)
		if schema["type"] != "object" {
			return Seen{}, fmt.Errorf("tool %v: input_schema must be an object schema", tm["name"])
		}
		seen.Tools = append(seen.Tools, tm["name"].(string))
		seen.Schemas[tm["name"].(string)] = schema
	}
	messages, _ := req["messages"].([]any)
	if len(messages) == 0 {
		return Seen{}, errors.New("messages is empty")
	}
	pending := map[string]bool{} // tool_use IDs the next user message must answer
	for i, raw := range messages {
		msg := raw.(map[string]any)
		role, _ := msg["role"].(string)
		if role != "user" && role != "assistant" {
			return Seen{}, fmt.Errorf("messages.%d: unknown role %q", i, role)
		}
		if i == 0 && role != "user" {
			return Seen{}, errors.New("the first message must be a user message")
		}
		if i > 0 && messages[i-1].(map[string]any)["role"] == role {
			return Seen{}, fmt.Errorf("messages.%d: roles must alternate", i)
		}
		blocks, ok := msg["content"].([]any)
		if !ok || len(blocks) == 0 {
			return Seen{}, fmt.Errorf("messages.%d: content must be a non-empty list", i)
		}
		m := SeenMessage{Role: role}
		answered := map[string]bool{}
		nonResultSeen := false
		for _, rb := range blocks {
			b := rb.(map[string]any)
			switch b["type"] {
			case "text":
				text, _ := b["text"].(string)
				if text == "" {
					return Seen{}, fmt.Errorf("messages.%d: text blocks must be non-empty", i)
				}
				m.Texts = append(m.Texts, text)
				nonResultSeen = true
			case "image", "document":
				src := b["source"].(map[string]any)
				mime := src["media_type"].(string)
				if b["type"] == "image" && !anthropicImageTypes[mime] {
					return Seen{}, fmt.Errorf("messages.%d: image media_type %s is not supported", i, mime)
				}
				if b["type"] == "document" && mime != "application/pdf" {
					return Seen{}, fmt.Errorf("messages.%d: document media_type %s is not supported", i, mime)
				}
				data, err := base64.StdEncoding.DecodeString(src["data"].(string))
				if err != nil {
					return Seen{}, err
				}
				m.Attachments = append(m.Attachments, Attachment{MimeType: mime, Data: data})
				nonResultSeen = true
			case "tool_use":
				if role != "assistant" {
					return Seen{}, fmt.Errorf("messages.%d: tool_use in a user message", i)
				}
				id, _ := b["id"].(string)
				input, ok := b["input"].(map[string]any)
				if id == "" || !ok {
					return Seen{}, fmt.Errorf("messages.%d: tool_use needs an id and an input object", i)
				}
				m.Calls = append(m.Calls, SeenCall{ID: id, Name: b["name"].(string), Args: input})
			case "tool_result":
				id, _ := b["tool_use_id"].(string)
				if role != "user" || !pending[id] {
					return Seen{}, fmt.Errorf("messages.%d: tool_result %q does not answer a tool_use of the previous message", i, id)
				}
				if nonResultSeen {
					return Seen{}, fmt.Errorf("messages.%d: tool_result blocks must come first", i)
				}
				answered[id] = true
				content, _ := b["content"].(string)
				m.Results = append(m.Results, SeenResult{ID: id, Content: content})
			default:
				return Seen{}, fmt.Errorf("messages.%d: unknown block type %v", i, b["type"])
			}
		}
		if role == "user" {
			for id := range pending {
				if !answered[id] {
					return Seen{}, fmt.Errorf("messages.%d: tool_use %s has no tool_result", i, id)
				}
			}
			pending = map[string]bool{}
		} else {
			for _, c := range m.Calls {
				pending[c.ID] = true
			}
		}
		seen.Messages = append(seen.Messages, m)
	}
	if len(pending) > 0 {
		return Seen{}, errors.New("the last message has unanswered tool_use blocks")
	}
	return seen, nil
}

func anthropicReply(r Reply) []byte {
	// Anthropic counts prompt-cache reads apart from input_tokens; the fake reports one input
	// token as a cache read when it has more than one, so the adapter must add them.
	cached := 0
	if r.InputTokens > 1 {
		cached = 1
	}
	usage := map[string]any{"input_tokens": r.InputTokens - cached, "cache_read_input_tokens": cached,
		"cache_creation_input_tokens": 0, "output_tokens": r.OutputTokens}
	switch {
	case r.Blocked:
		return mustJSON(map[string]any{"type": "message", "role": "assistant", "content": []any{}, "stop_reason": "refusal", "usage": usage})
	case r.Call != nil:
		return mustJSON(map[string]any{"type": "message", "role": "assistant", "stop_reason": "tool_use", "usage": usage,
			"content": []any{map[string]any{"type": "tool_use", "id": "toolu_fake_1", "name": r.Call.Name, "input": r.Call.Args}}})
	}
	return mustJSON(map[string]any{"type": "message", "role": "assistant", "stop_reason": "end_turn", "usage": usage,
		"content": []any{map[string]any{"type": "text", "text": r.Text}}})
}

// ---------------------------------------------------------------- OpenAI-compatible

func parseOpenAI(r *http.Request, body []byte, needsKey bool) (Seen, error) {
	if !strings.HasSuffix(r.URL.Path, "/v1/chat/completions") {
		return Seen{}, fmt.Errorf("unexpected path %s", r.URL.Path)
	}
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if needsKey && auth == "" {
		return Seen{}, errors.New("missing bearer key")
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return Seen{}, err
	}
	seen := Seen{Schemas: map[string]map[string]any{}, Auth: auth, Sampling: samplingIn(req)}
	seen.Model, _ = req["model"].(string)
	tools, _ := req["tools"].([]any)
	for _, t := range tools {
		fn := t.(map[string]any)["function"].(map[string]any)
		schema, _ := fn["parameters"].(map[string]any)
		seen.Tools = append(seen.Tools, fn["name"].(string))
		seen.Schemas[fn["name"].(string)] = schema
	}
	messages, _ := req["messages"].([]any)
	if len(messages) == 0 {
		return Seen{}, errors.New("messages is empty")
	}
	pending := map[string]bool{}
	var last *SeenMessage
	for i, raw := range messages {
		msg := raw.(map[string]any)
		role, _ := msg["role"].(string)
		if role != "tool" && len(pending) > 0 {
			return Seen{}, fmt.Errorf("messages.%d: an assistant message with tool_calls must be followed by a tool message for each call", i)
		}
		switch role {
		case "system":
			if i != 0 {
				return Seen{}, errors.New("the system message must come first")
			}
			seen.System, _ = msg["content"].(string)
		case "assistant":
			m := SeenMessage{Role: "assistant"}
			if text, ok := msg["content"].(string); ok && text != "" {
				m.Texts = append(m.Texts, text)
			}
			calls, _ := msg["tool_calls"].([]any)
			for _, rc := range calls {
				c := rc.(map[string]any)
				fn := c["function"].(map[string]any)
				argText, ok := fn["arguments"].(string)
				if !ok {
					return Seen{}, fmt.Errorf("messages.%d: function.arguments must be a JSON string", i)
				}
				var args map[string]any
				if err := json.Unmarshal([]byte(argText), &args); err != nil {
					return Seen{}, fmt.Errorf("messages.%d: function.arguments is not a JSON object: %w", i, err)
				}
				id, _ := c["id"].(string)
				m.Calls = append(m.Calls, SeenCall{ID: id, Name: fn["name"].(string), Args: args})
				pending[id] = true
			}
			seen.Messages = append(seen.Messages, m)
			last = nil
		case "tool":
			id, _ := msg["tool_call_id"].(string)
			if !pending[id] {
				return Seen{}, fmt.Errorf("messages.%d: tool message %q does not answer a call of the previous assistant message", i, id)
			}
			delete(pending, id)
			content, _ := msg["content"].(string)
			if last == nil {
				seen.Messages = append(seen.Messages, SeenMessage{Role: "user"})
				last = &seen.Messages[len(seen.Messages)-1]
			}
			last.Results = append(last.Results, SeenResult{ID: id, Content: content})
		case "user":
			m := SeenMessage{Role: "user"}
			switch content := msg["content"].(type) {
			case string:
				m.Texts = append(m.Texts, content)
			case []any:
				for _, rp := range content {
					p := rp.(map[string]any)
					switch p["type"] {
					case "text":
						m.Texts = append(m.Texts, p["text"].(string))
					case "image_url":
						a, err := dataURL(p["image_url"].(map[string]any)["url"].(string))
						if err != nil {
							return Seen{}, err
						}
						m.Attachments = append(m.Attachments, a)
					case "file":
						a, err := dataURL(p["file"].(map[string]any)["file_data"].(string))
						if err != nil {
							return Seen{}, err
						}
						m.Attachments = append(m.Attachments, a)
					default:
						return Seen{}, fmt.Errorf("messages.%d: unknown content part %v", i, p["type"])
					}
				}
			}
			if last != nil { // a user message right after tool results: same turn
				last.Texts = append(last.Texts, m.Texts...)
				last.Attachments = append(last.Attachments, m.Attachments...)
			} else {
				seen.Messages = append(seen.Messages, m)
			}
			last = nil
		default:
			return Seen{}, fmt.Errorf("messages.%d: unknown role %q", i, role)
		}
	}
	if len(pending) > 0 {
		return Seen{}, errors.New("unanswered tool_calls at the end of the conversation")
	}
	return seen, nil
}

func dataURL(raw string) (Attachment, error) {
	rest, ok := strings.CutPrefix(raw, "data:")
	mime, encoded, ok2 := strings.Cut(rest, ";base64,")
	if !ok || !ok2 {
		return Attachment{}, fmt.Errorf("not a base64 data URL")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Attachment{}, err
	}
	return Attachment{MimeType: mime, Data: data}, nil
}

func openAIReply(r Reply) []byte {
	usage := map[string]any{"prompt_tokens": r.InputTokens, "completion_tokens": r.OutputTokens,
		"total_tokens": r.InputTokens + r.OutputTokens}
	switch {
	case r.Blocked:
		return mustJSON(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "content_filter",
			"message": map[string]any{"role": "assistant", "content": nil}}}, "usage": usage})
	case r.Call != nil:
		return mustJSON(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls",
			"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
				"id": "call_fake_1", "type": "function", "function": map[string]any{"name": r.Call.Name, "arguments": string(mustJSON(r.Call.Args))}}}}}},
			"usage": usage})
	}
	return mustJSON(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
		"message": map[string]any{"role": "assistant", "content": r.Text}}}, "usage": usage})
}
