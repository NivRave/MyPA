package llm

import "github.com/nivik/mypa/internal/config"

// Support is how far a provider supports a capability.
type Support string

const (
	// Supported: the provider's API takes it, and every current chat model of the provider does.
	Supported Support = "yes"
	// ModelDependent: the API takes it, but whether it works depends on the configured model
	// (an Ollama model without vision, a Grok model without images).
	ModelDependent Support = "depends on the model"
	// Unsupported: the API does not take it. The adapter degrades in a documented way instead
	// of failing the turn (an attachment becomes a note telling the model it cannot read it).
	Unsupported Support = "no"
)

// Capabilities is what a provider declares. The conformance suite (internal/llm/conformance)
// checks each declaration against the adapter's wire behaviour, and the capability matrix in
// SYSTEM.md is generated from the declarations whose scenarios pass.
type Capabilities struct {
	// Wire is the API format the adapter speaks.
	Wire string
	// ToolCalls, Images and PDF are the support for tool calling, images and PDF documents.
	ToolCalls, Images, PDF Support
	// Local reports whether the model runs on a machine the install controls.
	Local bool
}

// The wire formats.
const (
	WireGemini    = "Gemini API (genai SDK)"
	WireAnthropic = "Anthropic Messages API"
	WireOpenAI    = "OpenAI-compatible chat completions"
)

// CapabilitiesOf returns the declared capabilities of a provider by name.
func CapabilitiesOf(provider string) Capabilities {
	switch provider {
	case config.ProviderGemini:
		return Capabilities{Wire: WireGemini, ToolCalls: Supported, Images: Supported, PDF: Supported}
	case config.ProviderAnthropic:
		return Capabilities{Wire: WireAnthropic, ToolCalls: Supported, Images: Supported, PDF: Supported}
	case config.ProviderOpenAI:
		return Capabilities{Wire: WireOpenAI, ToolCalls: Supported, Images: Supported, PDF: Supported}
	case config.ProviderGrok:
		return Capabilities{Wire: WireOpenAI, ToolCalls: Supported, Images: ModelDependent, PDF: Unsupported}
	case config.ProviderOllama:
		return Capabilities{Wire: WireOpenAI, ToolCalls: ModelDependent, Images: ModelDependent, PDF: Unsupported, Local: true}
	}
	return Capabilities{}
}
