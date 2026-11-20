package conformance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/llm"
)

// FakeConfig is a configuration that names every provider, with test keys and models, and Ollama
// at OllamaURL. The Gemini model is left unset, so the default applies (V4-09).
func FakeConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Gemini.APIKey = "test-key-gemini"
	cfg.Anthropic.APIKey, cfg.Anthropic.Model = "test-key-anthropic", "test-model-anthropic"
	cfg.OpenAI.APIKey, cfg.OpenAI.Model = "test-key-openai", "test-model-openai"
	cfg.XAI.APIKey, cfg.XAI.Model = "test-key-grok", "test-model-grok"
	cfg.Ollama.BaseURL, cfg.Ollama.Model = OllamaURL, "test-model-ollama"
	return cfg
}

// Result is one scenario's outcome for one provider.
type Result struct {
	Provider, Scenario string
	// Skipped is set for a scenario that needs a fake, in a live run.
	Skipped bool
	Err     error
}

// RunFakes runs every scenario against every provider's fake and returns the results, provider
// by provider in the order of config.Providers.
func RunFakes(ctx context.Context) ([]Result, error) {
	fakes := NewFakes()
	cfg := FakeConfig()
	var results []Result
	for _, name := range config.Providers() {
		p, err := llm.NewProvider(ctx, cfg, name, llm.WithHTTPClient(fakes.Client()))
		if err != nil {
			return nil, fmt.Errorf("build %s: %w", name, err)
		}
		env := Env{Provider: p, Fake: fakes.Of(name), Key: keyOf(cfg, name)}
		for _, sc := range Scenarios() {
			env.Fake.Reset()
			results = append(results, Result{Provider: name, Scenario: sc.ID, Err: sc.Run(ctx, env)})
		}
	}
	return results, nil
}

// RunLive runs the scenarios that need no fake against a real provider. Each scenario gets its
// own timeout; nothing is retried.
func RunLive(ctx context.Context, p llm.Provider, key string) []Result {
	env := Env{Provider: p, Key: key}
	var results []Result
	for _, sc := range Scenarios() {
		if sc.FakeOnly {
			results = append(results, Result{Provider: p.Name(), Scenario: sc.ID, Skipped: true})
			continue
		}
		scCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		results = append(results, Result{Provider: p.Name(), Scenario: sc.ID, Err: sc.Run(scCtx, env)})
		cancel()
	}
	return results
}

// Matrix renders the capability matrix (SYSTEM.md region "llm-provider-matrix"): one column per
// provider, a row per scenario showing the declared support it proves, and the facts the
// declarations hold. It fails when any scenario fails against the fakes, so the matrix never
// claims what the adapters do not do.
func Matrix(ctx context.Context) (string, error) {
	results, err := RunFakes(ctx)
	if err != nil {
		return "", err
	}
	var failed []string
	for _, r := range results {
		if r.Err != nil {
			failed = append(failed, fmt.Sprintf("%s/%s: %v", r.Provider, r.Scenario, r.Err))
		}
	}
	if len(failed) > 0 {
		return "", fmt.Errorf("provider conformance failed, so the matrix cannot be generated:\n  %s", strings.Join(failed, "\n  "))
	}

	providers := config.Providers()
	var b strings.Builder
	b.WriteString("| Capability |")
	for _, p := range providers {
		fmt.Fprintf(&b, " `%s` |", p)
	}
	b.WriteString("\n| :--- |")
	for range providers {
		b.WriteString(" :--- |")
	}
	b.WriteString("\n")
	row := func(title string, cell func(name string, c llm.Capabilities) string) {
		fmt.Fprintf(&b, "| %s |", title)
		for _, p := range providers {
			fmt.Fprintf(&b, " %s |", cell(p, llm.CapabilitiesOf(p)))
		}
		b.WriteString("\n")
	}
	row("Wire format", func(_ string, c llm.Capabilities) string { return c.Wire })
	row("Runs on this machine (may take turns kept local)", func(_ string, c llm.Capabilities) string { return yesNo(c.Local) })
	for _, sc := range Scenarios() {
		sc := sc
		row(sc.Title, func(_ string, c llm.Capabilities) string {
			if sc.Support == nil {
				return string(llm.Supported)
			}
			if s := sc.Support(c); s == llm.Unsupported {
				return "no: the model is told it cannot read it"
			} else {
				return string(s)
			}
		})
	}
	row("Embeddings (V4-03)", func(name string, _ llm.Capabilities) string {
		return yesNo(name == config.ProviderGemini)
	})
	return strings.TrimRight(b.String(), "\n"), nil
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
