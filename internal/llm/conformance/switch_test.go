package conformance

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// providerEnv is every setting that chooses or configures a chat provider. Each test sets all of
// them, so a developer's own environment cannot leak in.
var providerEnv = []string{
	"LLM_PROVIDER", "LLM_FALLBACK", "LLM_LOCAL_DOMAINS", "GEMINI_MODEL",
	"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "OPENAI_API_KEY", "OPENAI_MODEL",
	"XAI_API_KEY", "XAI_MODEL", "OLLAMA_BASE_URL", "OLLAMA_MODEL",
}

// loadWith loads the configuration the way the app does (config.Load) from these settings on top
// of a valid worker configuration, and checks that the app would start with it.
func loadWith(t *testing.T, settings map[string]string) *config.Config {
	t.Helper()
	t.Chdir(t.TempDir()) // no .env and no config file in reach
	base := map[string]string{
		"ENCRYPTION_KEY": strings.Repeat("e", 64), "DATABASE_URL": "postgres://u:p@db:5432/mypa",
		"REDIS_URL": "redis://redis:6379/0", "RABBITMQ_URL": "amqp://u:p@mq:5672/", "GEMINI_API_KEY": "test-key-gemini",
		"INGRESS": "none", "PUBLIC_URL": "",
	}
	for _, k := range providerEnv {
		base[k] = ""
	}
	for k, v := range settings {
		base[k] = v
	}
	for k, v := range base {
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	require.NoError(t, err)
	require.NoError(t, cfg.Validate(config.RoleWorker), "the app must start with these settings")
	return cfg
}

func chatOnce(t *testing.T, client *llm.Client) *llm.Response {
	t.Helper()
	resp, err := client.Chat(context.Background(), "You are a family assistant.", nil, "שלום", nil, "")
	require.NoError(t, err)
	return resp
}

// V4-02 "done when", first half: switching provider is a config change. The same build and the
// same code path (config.Load, then llm.NewFromConfig, which the worker calls) reach each provider
// with only the settings changed; the fakes stand in for the network at the real addresses.
func TestSwitchingProviderIsAConfigChange(t *testing.T) {
	cases := map[string]map[string]string{
		config.ProviderGemini:    {},
		config.ProviderAnthropic: {"LLM_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "test-key-anthropic", "ANTHROPIC_MODEL": "test-model-anthropic"},
		config.ProviderOpenAI:    {"LLM_PROVIDER": "openai", "OPENAI_API_KEY": "test-key-openai", "OPENAI_MODEL": "test-model-openai"},
		config.ProviderGrok:      {"LLM_PROVIDER": "grok", "XAI_API_KEY": "test-key-grok", "XAI_MODEL": "test-model-grok"},
		config.ProviderOllama:    {"LLM_PROVIDER": "ollama", "OLLAMA_BASE_URL": OllamaURL, "OLLAMA_MODEL": "test-model-ollama"},
	}
	require.Len(t, cases, len(config.Providers()), "every provider is covered")
	for want, settings := range cases {
		t.Run(want, func(t *testing.T) {
			cfg := loadWith(t, settings)
			fakes := NewFakes()
			client, err := llm.NewFromConfig(context.Background(), cfg, llm.WithHTTPClient(fakes.Client()))
			require.NoError(t, err)

			resp := chatOnce(t, client)
			assert.Equal(t, want, resp.Provider, "the configured provider answered")
			for _, name := range config.Providers() {
				got := len(fakes.Of(name).Requests())
				if name == want {
					require.Equal(t, 1, got, "%s must receive the request", name)
					seen, _ := fakes.Of(name).LastRequest()
					assert.Equal(t, cfg.ProviderModel(name), seen.Model, "with the configured model")
					if name == config.ProviderGemini {
						assert.Equal(t, config.DefaultGeminiModel, seen.Model, "an install that sets nothing keeps the default")
					}
				} else {
					assert.Zero(t, got, "%s must not be called", name)
				}
			}
		})
	}
}

// A provider named without its settings, or a name that is not a provider, is refused at start
// with a message naming what is missing, never discovered at the first message.
func TestProviderSettingsAreCheckedAtStart(t *testing.T) {
	cases := map[string]struct {
		settings map[string]string
		want     string
	}{
		"unknown provider":  {map[string]string{"LLM_PROVIDER": "chatgpt"}, `unknown provider "chatgpt"`},
		"no model":          {map[string]string{"LLM_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "k"}, "ANTHROPIC_MODEL is required"},
		"no key":            {map[string]string{"LLM_PROVIDER": "openai", "OPENAI_MODEL": "m"}, "OPENAI_API_KEY is required"},
		"fallback no key":   {map[string]string{"LLM_FALLBACK": "grok", "XAI_MODEL": "m"}, "XAI_API_KEY is required"},
		"repeated":          {map[string]string{"LLM_FALLBACK": "gemini"}, "names gemini twice"},
		"local needs model": {map[string]string{"LLM_LOCAL_DOMAINS": "finance", "OLLAMA_BASE_URL": OllamaURL}, "OLLAMA_MODEL is required"},
		"bad ollama url":    {map[string]string{"LLM_PROVIDER": "ollama", "OLLAMA_BASE_URL": "ollama:11434", "OLLAMA_MODEL": "m"}, "OLLAMA_BASE_URL must be an http"},
		"placeholder key":   {map[string]string{"LLM_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "your-anthropic-key", "ANTHROPIC_MODEL": "m"}, "placeholder"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			for _, k := range providerEnv {
				t.Setenv(k, tc.settings[k])
			}
			for k, v := range map[string]string{"ENCRYPTION_KEY": strings.Repeat("e", 64), "DATABASE_URL": "postgres://db", "REDIS_URL": "redis://r",
				"RABBITMQ_URL": "amqp://mq", "GEMINI_API_KEY": "k", "INGRESS": "none", "PUBLIC_URL": ""} {
				t.Setenv(k, v)
			}
			cfg, err := config.Load()
			require.NoError(t, err)
			err = cfg.Validate(config.RoleWorker)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// V4-02 "done when", second half: a failover test passes with the primary disabled. Gemini is the
// primary and Anthropic the fallback, configured by settings alone. With Gemini unreachable, or
// out of quota, the turn is answered by Anthropic; while Gemini is in its cooldown the next call
// goes straight to Anthropic; once the cooldown has passed Gemini is tried first again.
func TestFailoverWithThePrimaryDisabled(t *testing.T) {
	for name, disabled := range map[string]Reply{
		"unreachable":     {Down: true},
		"quota exhausted": {Status: http.StatusTooManyRequests},
		"outage":          {Status: http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := loadWith(t, map[string]string{"LLM_FALLBACK": "anthropic",
				"ANTHROPIC_API_KEY": "test-key-anthropic", "ANTHROPIC_MODEL": "test-model-anthropic"})
			fakes := NewFakes()
			client, err := llm.NewFromConfig(context.Background(), cfg, llm.WithHTTPClient(fakes.Client()))
			require.NoError(t, err)
			now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
			client.Router().WithClock(func() time.Time { return now })
			gemini, anthropic := fakes.Of(config.ProviderGemini), fakes.Of(config.ProviderAnthropic)

			gemini.Answer(disabled)
			anthropic.Answer(Reply{Text: "answered by the fallback"})
			resp := chatOnce(t, client)
			assert.Equal(t, config.ProviderAnthropic, resp.Provider)
			assert.Equal(t, "answered by the fallback", resp.Text)
			if !disabled.Down {
				assert.Len(t, gemini.Requests(), 1, "the primary was tried first")
			}
			assert.Len(t, anthropic.Requests(), 1)

			// In the cooldown the fallback is asked first.
			gemini.Reset()
			gemini.Answer(disabled)
			resp = chatOnce(t, client)
			assert.Equal(t, config.ProviderAnthropic, resp.Provider)
			assert.Empty(t, gemini.Requests(), "the primary is skipped while it is in its cooldown")

			// After the cooldown, and with the primary back, the primary answers again.
			now = now.Add(llm.DefaultCooldown + time.Second)
			gemini.Answer(Reply{Text: "primary is back"})
			resp = chatOnce(t, client)
			assert.Equal(t, config.ProviderGemini, resp.Provider)
			assert.Equal(t, "primary is back", resp.Text)
		})
	}
}

// When every provider is unavailable the turn fails with an error that says so, and a provider
// in its cooldown is still tried when there is nothing else.
func TestFailover_EveryProviderDown(t *testing.T) {
	cfg := loadWith(t, map[string]string{"LLM_FALLBACK": "openai", "OPENAI_API_KEY": "k", "OPENAI_MODEL": "m"})
	fakes := NewFakes()
	client, err := llm.NewFromConfig(context.Background(), cfg, llm.WithHTTPClient(fakes.Client()))
	require.NoError(t, err)
	fakes.Of(config.ProviderGemini).Answer(Reply{Status: 503})
	fakes.Of(config.ProviderOpenAI).Answer(Reply{Status: 429})
	_, err = client.Chat(context.Background(), "s", nil, "m", nil, "")
	require.Error(t, err)
	assert.True(t, llm.IsUnavailable(err))
	assert.Contains(t, err.Error(), "every llm provider is unavailable")

	fakes.Of(config.ProviderOpenAI).Answer(Reply{Text: "back"})
	resp := chatOnce(t, client)
	assert.Equal(t, config.ProviderOpenAI, resp.Provider, "providers in their cooldown are still tried when all are in one")
}

// A blocked prompt and a refused request are final: the fallback is never asked, so a safety
// block cannot be routed around and a malformed request is not sent twice.
func TestFailover_NotOnABlockOrABadRequest(t *testing.T) {
	for name, reply := range map[string]Reply{"blocked": {Blocked: true}, "bad request": {Status: 400}} {
		t.Run(name, func(t *testing.T) {
			cfg := loadWith(t, map[string]string{"LLM_FALLBACK": "anthropic", "ANTHROPIC_API_KEY": "k", "ANTHROPIC_MODEL": "m"})
			fakes := NewFakes()
			client, err := llm.NewFromConfig(context.Background(), cfg, llm.WithHTTPClient(fakes.Client()))
			require.NoError(t, err)
			fakes.Of(config.ProviderGemini).Answer(reply)
			_, err = client.Chat(context.Background(), "s", nil, "m", nil, "")
			require.Error(t, err)
			assert.Empty(t, fakes.Of(config.ProviderAnthropic).Requests(), "the fallback must not be asked")
			var blocked *llm.ContentBlockedError
			assert.Equal(t, reply.Blocked, errors.As(err, &blocked))
		})
	}
}

// A turn kept local goes to Ollama only. When Ollama is down the turn fails; it is never sent to
// the cloud primary or a cloud fallback.
func TestLocalTurnsNeverLeaveTheMachine(t *testing.T) {
	cfg := loadWith(t, map[string]string{"LLM_FALLBACK": "anthropic", "ANTHROPIC_API_KEY": "k", "ANTHROPIC_MODEL": "m",
		"LLM_LOCAL_DOMAINS": "finance", "OLLAMA_BASE_URL": OllamaURL, "OLLAMA_MODEL": "test-model-ollama"})
	fakes := NewFakes()
	client, err := llm.NewFromConfig(context.Background(), cfg, llm.WithHTTPClient(fakes.Client()))
	require.NoError(t, err)
	local := llm.WithLocalOnly(context.Background())

	resp, err := client.Chat(local, "s", nil, "how much did we spend this month?", nil, "")
	require.NoError(t, err)
	assert.Equal(t, config.ProviderOllama, resp.Provider)

	fakes.Of(config.ProviderOllama).Answer(Reply{Down: true})
	_, err = client.Chat(local, "s", nil, "and last month?", nil, "")
	require.Error(t, err)
	assert.Empty(t, fakes.Of(config.ProviderGemini).Requests(), "a local turn must never reach the cloud primary")
	assert.Empty(t, fakes.Of(config.ProviderAnthropic).Requests(), "or a cloud fallback")

	// Any other turn still uses the chain.
	resp = chatOnce(t, client)
	assert.Equal(t, config.ProviderGemini, resp.Provider)
}

// Without a local provider, a turn marked local is refused rather than sent to the cloud.
func TestLocalTurnWithoutALocalProviderIsRefused(t *testing.T) {
	cfg := loadWith(t, nil)
	fakes := NewFakes()
	client, err := llm.NewFromConfig(context.Background(), cfg, llm.WithHTTPClient(fakes.Client()))
	require.NoError(t, err)
	_, err = client.Chat(llm.WithLocalOnly(context.Background()), "s", nil, "m", nil, "")
	require.ErrorIs(t, err, llm.ErrNoLocalProvider)
	assert.Empty(t, fakes.Of(config.ProviderGemini).Requests())
}

// The tool loop of a turn survives a failover in the middle: the history with the primary's tool
// call and its result is valid for the fallback's API.
func TestFailoverInTheMiddleOfAToolLoop(t *testing.T) {
	cfg := loadWith(t, map[string]string{"LLM_FALLBACK": "openai", "OPENAI_API_KEY": "k", "OPENAI_MODEL": "m"})
	fakes := NewFakes()
	client, err := llm.NewFromConfig(context.Background(), cfg, llm.WithHTTPClient(fakes.Client()))
	require.NoError(t, err)
	fakes.Of(config.ProviderGemini).Answer(Reply{Call: &Call{Name: "add_shopping_item", Args: map[string]any{"item": "milk"}}})
	resp, err := client.Chat(context.Background(), "s", nil, "add milk", nil, "")
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)

	history := []models.ChatMessage{
		{Role: "user", Content: "add milk"},
		{Role: "assistant", ToolCalls: []models.FunctionCall{{Name: resp.ToolCalls[0].Name, Args: resp.ToolCalls[0].Args}}},
		{Role: "function", ToolResponses: []models.FunctionResponse{{Name: resp.ToolCalls[0].Name, Response: map[string]any{"result": "added"}}}},
	}
	fakes.Of(config.ProviderGemini).Answer(Reply{Status: 503})
	fakes.Of(config.ProviderOpenAI).Answer(Reply{Text: "Milk is on the list."})
	resp, err = client.Chat(context.Background(), "s", history, "continue", nil, "")
	require.NoError(t, err, "the fallback's fake refuses a history its API would refuse")
	assert.Equal(t, config.ProviderOpenAI, resp.Provider)
}

// The fakes refuse what the real APIs refuse, so a passing scenario means a valid request.
func TestTheFakesRefuseMalformedRequests(t *testing.T) {
	fakes := NewFakes()
	post := func(url, body string, header map[string]string) int {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
		require.NoError(t, err)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		res, err := fakes.Client().Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		return res.StatusCode
	}
	anthropic := map[string]string{"x-api-key": "k", "anthropic-version": "2023-06-01"}
	openai := map[string]string{"Authorization": "Bearer k"}
	for name, tc := range map[string]struct {
		url, body string
		header    map[string]string
	}{
		"anthropic: result without its call": {llm.AnthropicURL, `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"r"}]}]}`, anthropic},
		"anthropic: call without its result": {llm.AnthropicURL, `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"a"}]},{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"t","input":{}}]},{"role":"user","content":[{"type":"text","text":"b"}]}]}`, anthropic},
		"anthropic: roles repeat":            {llm.AnthropicURL, `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"a"}]},{"role":"user","content":[{"type":"text","text":"b"}]}]}`, anthropic},
		"anthropic: no max_tokens":           {llm.AnthropicURL, `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"a"}]}]}`, anthropic},
		"anthropic: no version header":       {llm.AnthropicURL, `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"a"}]}]}`, map[string]string{"x-api-key": "k"}},
		"openai: tool message without call":  {llm.OpenAIURL, `{"model":"m","messages":[{"role":"user","content":"a"},{"role":"tool","tool_call_id":"x","content":"r"}]}`, openai},
		"openai: call without tool message":  {llm.OpenAIURL, `{"model":"m","messages":[{"role":"user","content":"a"},{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"t","arguments":"{}"}}]},{"role":"user","content":"b"}]}`, openai},
		"openai: arguments not a string":     {llm.OpenAIURL, `{"model":"m","messages":[{"role":"user","content":"a"},{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"t","arguments":{}}}]},{"role":"tool","tool_call_id":"c1","content":"r"}]}`, openai},
		"openai: no key":                     {llm.OpenAIURL, `{"model":"m","messages":[{"role":"user","content":"a"}]}`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, http.StatusBadRequest, post(tc.url, tc.body, tc.header))
		})
	}
	assert.Equal(t, http.StatusOK, post(llm.AnthropicURL, `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"a"}]}]}`, anthropic), "a valid request is accepted")
	assert.Equal(t, http.StatusOK, post(llm.OpenAIURL, `{"model":"m","messages":[{"role":"user","content":"a"}]}`, openai), "a valid request is accepted")
}

// The matrix renders a column per provider and fails instead of claiming what does not pass.
func TestMatrixHasAColumnPerProvider(t *testing.T) {
	m, err := Matrix(context.Background())
	require.NoError(t, err)
	header := strings.SplitN(m, "\n", 2)[0]
	for _, p := range config.Providers() {
		assert.Contains(t, header, "`"+p+"`")
	}
	for _, sc := range Scenarios() {
		assert.Contains(t, m, "| "+sc.Title+" |")
	}
}
