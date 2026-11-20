package llm

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nivik/mypa/internal/config"
	"google.golang.org/genai"
)

// Option changes how NewFromConfig reaches the providers.
type Option func(*setup)

type setup struct {
	httpClient *http.Client
}

// WithHTTPClient sends every provider's requests through client. The conformance suite and tests
// use it to put fake servers behind the real endpoint addresses; production passes none and each
// provider gets a client with the default timeout.
func WithHTTPClient(client *http.Client) Option {
	return func(s *setup) { s.httpClient = client }
}

// NewProvider builds the named provider from the configuration.
func NewProvider(ctx context.Context, cfg *config.Config, name string, opts ...Option) (Provider, error) {
	s := &setup{}
	for _, o := range opts {
		o(s)
	}
	switch name {
	case config.ProviderGemini:
		var geminiOpts []func(*genai.ClientConfig)
		if s.httpClient != nil {
			geminiOpts = append(geminiOpts, func(c *genai.ClientConfig) { c.HTTPClient = s.httpClient })
		}
		return NewGeminiProvider(ctx, cfg.Gemini.APIKey, cfg.Gemini.Model, geminiOpts...)
	case config.ProviderAnthropic:
		return NewAnthropicProvider(cfg.Anthropic.APIKey, cfg.Anthropic.Model, s.httpClient)
	case config.ProviderOpenAI:
		return NewOpenAIProvider(cfg.OpenAI.APIKey, cfg.OpenAI.Model, s.httpClient)
	case config.ProviderGrok:
		return NewGrokProvider(cfg.XAI.APIKey, cfg.XAI.Model, s.httpClient)
	case config.ProviderOllama:
		return NewOllamaProvider(cfg.Ollama.BaseURL, cfg.Ollama.Model, s.httpClient)
	}
	return nil, fmt.Errorf("unknown llm provider %q", name)
}

// NewFromConfig builds the chat client the configuration describes (V4-02): the router over
// LLM_PROVIDER and LLM_FALLBACK, and Ollama as the local provider when LLM_LOCAL_DOMAINS keeps
// turns local. Switching provider is a change of settings only. The embedder is set separately
// (UseEmbedder, V4-03).
func NewFromConfig(ctx context.Context, cfg *config.Config, opts ...Option) (*Client, error) {
	built := map[string]Provider{}
	get := func(name string) (Provider, error) {
		if p, ok := built[name]; ok {
			return p, nil
		}
		p, err := NewProvider(ctx, cfg, name, opts...)
		if err != nil {
			return nil, fmt.Errorf("llm provider %s: %w", name, err)
		}
		built[name] = p
		return p, nil
	}
	var chain []Provider
	for _, name := range cfg.ProviderChain() {
		p, err := get(name)
		if err != nil {
			return nil, err
		}
		chain = append(chain, p)
	}
	var local Provider
	if len(cfg.LocalDomains()) > 0 {
		p, err := get(config.ProviderOllama)
		if err != nil {
			return nil, err
		}
		local = p
	}
	router, err := NewRouter(chain, local)
	if err != nil {
		return nil, err
	}
	return NewClientWithRouter(router), nil
}
