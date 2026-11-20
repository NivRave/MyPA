package config

import (
	"fmt"
	"net/url"
	"strings"
)

// The chat model providers (V4-02, DE-3). LLM_PROVIDER names the primary one and LLM_FALLBACK the
// ones tried after it, in order, when it is unavailable.
const (
	ProviderGemini    = "gemini"
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	// ProviderGrok is xAI's Grok. Its settings are XAI_API_KEY and XAI_MODEL, so they cannot be
	// mistaken for GROQ_API_KEY (voice transcription, another company).
	ProviderGrok   = "grok"
	ProviderOllama = "ollama"
)

// Providers returns the provider names LLM_PROVIDER and LLM_FALLBACK accept, in the order the docs
// list them.
func Providers() []string {
	return []string{ProviderGemini, ProviderAnthropic, ProviderOpenAI, ProviderGrok, ProviderOllama}
}

// LLMConfig chooses the chat model providers (V4-02). Switching provider is a change of these
// settings and of the chosen provider's own settings; nothing else changes. The embedding model
// is not chosen here (EmbeddingConfig, V4-03).
type LLMConfig struct {
	// Provider is the primary provider (LLM_PROVIDER). Empty: gemini.
	Provider string `mapstructure:"provider"`
	// Fallback lists the providers tried after the primary, in order, when it is unavailable
	// (LLM_FALLBACK, comma-separated). Empty: no failover.
	Fallback string `mapstructure:"fallback"`
	// LocalDomains lists the tool domains whose turns run only on the local model (Ollama), never
	// on a cloud provider (LLM_LOCAL_DOMAINS, comma-separated). Empty: no turn is kept local.
	LocalDomains string `mapstructure:"local_domains"`
}

// AnthropicConfig holds the Anthropic (Claude) settings. There is no built-in model: the one
// default chat model is Gemini's (V4-09), and a provider used without its model is refused.
type AnthropicConfig struct {
	APIKey string `mapstructure:"api_key"`
	Model  string `mapstructure:"model"`
}

// OpenAIConfig holds the OpenAI settings.
type OpenAIConfig struct {
	APIKey string `mapstructure:"api_key"`
	Model  string `mapstructure:"model"`
}

// XAIConfig holds the settings of xAI's Grok.
type XAIConfig struct {
	APIKey string `mapstructure:"api_key"`
	Model  string `mapstructure:"model"`
}

// OllamaConfig holds the settings of a local Ollama server. It needs no key.
type OllamaConfig struct {
	// BaseURL is the server's address, such as http://host.docker.internal:11434. There is no
	// default: from inside a container "localhost" is the container itself.
	BaseURL string `mapstructure:"base_url"`
	Model   string `mapstructure:"model"`
}

// llmEnvBindings maps the provider settings to their config keys.
var llmEnvBindings = map[string]string{
	"LLM_PROVIDER":      "llm.provider",
	"LLM_FALLBACK":      "llm.fallback",
	"LLM_LOCAL_DOMAINS": "llm.local_domains",
	"ANTHROPIC_API_KEY": "anthropic.api_key",
	"ANTHROPIC_MODEL":   "anthropic.model",
	"OPENAI_API_KEY":    "openai.api_key",
	"OPENAI_MODEL":      "openai.model",
	"XAI_API_KEY":       "xai.api_key",
	"XAI_MODEL":         "xai.model",
	"OLLAMA_BASE_URL":   "ollama.base_url",
	"OLLAMA_MODEL":      "ollama.model",
}

// llmConditionalKeys are the provider settings Validate demands when that provider is used.
func llmConditionalKeys() []string {
	return []string{"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "OPENAI_API_KEY", "OPENAI_MODEL",
		"XAI_API_KEY", "XAI_MODEL", "OLLAMA_BASE_URL", "OLLAMA_MODEL"}
}

// splitList splits a comma-separated setting into trimmed, lower-case, non-empty entries.
func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// PrimaryProvider is the provider LLM_PROVIDER names, gemini when it is not set.
func (c *Config) PrimaryProvider() string {
	if p := strings.ToLower(strings.TrimSpace(c.LLM.Provider)); p != "" {
		return p
	}
	return ProviderGemini
}

// ProviderChain is the primary provider followed by the fallback providers, in the order they are
// tried. Validate refuses an unknown or repeated name, so the chain of a valid configuration has
// each provider once.
func (c *Config) ProviderChain() []string {
	return append([]string{c.PrimaryProvider()}, splitList(c.LLM.Fallback)...)
}

// LocalDomains are the tool domains whose turns run only on the local model (LLM_LOCAL_DOMAINS).
func (c *Config) LocalDomains() []string {
	return splitList(c.LLM.LocalDomains)
}

// ProviderModel is the chat model a provider is configured with; empty when it has none.
func (c *Config) ProviderModel(provider string) string {
	switch provider {
	case ProviderGemini:
		return c.Gemini.Model
	case ProviderAnthropic:
		return c.Anthropic.Model
	case ProviderOpenAI:
		return c.OpenAI.Model
	case ProviderGrok:
		return c.XAI.Model
	case ProviderOllama:
		return c.Ollama.Model
	}
	return ""
}

func isProvider(name string) bool {
	for _, p := range Providers() {
		if p == name {
			return true
		}
	}
	return false
}

// validateLLM checks the provider settings of the worker (V4-02): every named provider exists and
// appears once, and every provider that can be called has what it needs. GEMINI_API_KEY is
// required in any case, for embeddings (V4-03).
func (c *Config) validateLLM() []error {
	var errs []error
	used := map[string]bool{}
	for i, name := range c.ProviderChain() {
		setting := "LLM_FALLBACK"
		if i == 0 {
			setting = "LLM_PROVIDER"
		}
		switch {
		case !isProvider(name):
			errs = append(errs, fmt.Errorf("%s names an unknown provider %q; the providers are %s", setting, name, strings.Join(Providers(), ", ")))
			continue
		case used[name]:
			errs = append(errs, fmt.Errorf("%s names %s twice: each provider is tried once (LLM_PROVIDER is the first)", setting, name))
			continue
		}
		used[name] = true
	}
	if len(c.LocalDomains()) > 0 {
		used[ProviderOllama] = true // local turns run on Ollama
	}
	need := func(provider, key, value string) {
		if !used[provider] {
			return
		}
		why := provider + " is named in LLM_PROVIDER or LLM_FALLBACK"
		if provider == ProviderOllama && len(c.LocalDomains()) > 0 {
			why = "LLM_LOCAL_DOMAINS keeps turns on the local model (ollama)"
		}
		switch {
		case strings.TrimSpace(value) == "":
			errs = append(errs, fmt.Errorf("%s is required: %s", key, why))
		case isPlaceholder(value):
			errs = append(errs, fmt.Errorf("%s still contains the .env.example placeholder value", key))
		}
	}
	need(ProviderAnthropic, "ANTHROPIC_API_KEY", c.Anthropic.APIKey)
	need(ProviderAnthropic, "ANTHROPIC_MODEL", c.Anthropic.Model)
	need(ProviderOpenAI, "OPENAI_API_KEY", c.OpenAI.APIKey)
	need(ProviderOpenAI, "OPENAI_MODEL", c.OpenAI.Model)
	need(ProviderGrok, "XAI_API_KEY", c.XAI.APIKey)
	need(ProviderGrok, "XAI_MODEL", c.XAI.Model)
	need(ProviderOllama, "OLLAMA_MODEL", c.Ollama.Model)
	need(ProviderOllama, "OLLAMA_BASE_URL", c.Ollama.BaseURL)
	if used[ProviderOllama] && strings.TrimSpace(c.Ollama.BaseURL) != "" {
		u, err := url.Parse(strings.TrimSpace(c.Ollama.BaseURL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("OLLAMA_BASE_URL must be an http or https address such as http://host.docker.internal:11434, got %q", c.Ollama.BaseURL))
		}
	}
	return errs
}
