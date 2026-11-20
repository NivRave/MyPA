// Command llmconformance runs the provider conformance scenarios (internal/llm/conformance, V4-02)
// against the real chat model providers: every provider whose settings are present in the
// environment (or .env), or only the ones named with -provider. The scenarios that need a fake (a
// safety block, an outage) are skipped. It costs a few small requests per provider.
//
//	go run ./cmd/devtools/llmconformance                    # every configured provider
//	go run ./cmd/devtools/llmconformance -provider anthropic,ollama
//
// It exits 1 when a scenario fails. Run it from the repository root.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/llm"
	"github.com/nivik/mypa/internal/llm/conformance"
)

func main() {
	only := flag.String("provider", "", "comma-separated providers to run (default: every configured one)")
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	cfg, err := config.Load()
	if err != nil {
		fail("load config: %v", err)
	}
	wanted := map[string]bool{}
	for _, p := range strings.Split(*only, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			wanted[p] = true
		}
	}

	ctx := context.Background()
	failed, ran := 0, 0
	for _, name := range config.Providers() {
		if len(wanted) > 0 && !wanted[name] {
			continue
		}
		key, ok := configured(cfg, name)
		if !ok {
			if len(wanted) > 0 {
				fail("%s is not configured (its key and model, or OLLAMA_BASE_URL and OLLAMA_MODEL)", name)
			}
			fmt.Printf("%-10s not configured, skipped\n", name)
			continue
		}
		p, err := llm.NewProvider(ctx, cfg, name)
		if err != nil {
			fail("%s: %v", name, err)
		}
		ran++
		fmt.Printf("%s (model %s)\n", name, p.Model())
		for _, r := range conformance.RunLive(ctx, p, key) {
			switch {
			case r.Skipped:
				fmt.Printf("  skip  %s (needs a fake)\n", r.Scenario)
			case r.Err != nil:
				failed++
				fmt.Printf("  FAIL  %s: %v\n", r.Scenario, r.Err)
			default:
				fmt.Printf("  ok    %s\n", r.Scenario)
			}
		}
	}
	if ran == 0 {
		fail("no provider is configured")
	}
	if failed > 0 {
		fmt.Printf("%d scenario(s) failed\n", failed)
		os.Exit(1)
	}
}

// configured reports whether a provider has the settings it needs, and returns its key.
func configured(cfg *config.Config, name string) (string, bool) {
	switch name {
	case config.ProviderGemini:
		return cfg.Gemini.APIKey, cfg.Gemini.APIKey != ""
	case config.ProviderAnthropic:
		return cfg.Anthropic.APIKey, cfg.Anthropic.APIKey != "" && cfg.Anthropic.Model != ""
	case config.ProviderOpenAI:
		return cfg.OpenAI.APIKey, cfg.OpenAI.APIKey != "" && cfg.OpenAI.Model != ""
	case config.ProviderGrok:
		return cfg.XAI.APIKey, cfg.XAI.APIKey != "" && cfg.XAI.Model != ""
	case config.ProviderOllama:
		return "", cfg.Ollama.BaseURL != "" && cfg.Ollama.Model != ""
	}
	return "", false
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "llmconformance: "+format+"\n", args...)
	os.Exit(2)
}
