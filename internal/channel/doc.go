// Package channel is the channel layer (ROADMAP V4-31, ADR 0003): one adapter per messaging
// platform behind a single interface.
//
// An adapter turns what its platform delivers into a neutral Inbound message and renders a neutral
// Outbound message in its platform's own way. It declares what it can do as data (Capabilities),
// and Render applies one documented fallback per missing capability before the adapter is called,
// so an adapter never receives what it cannot send.
//
// Nothing outside the adapters names a platform: the engine, the scheduler and the wiring see only
// the types of this package and the names adapters give themselves.
// TestNoPlatformNamedOutsideAdapters enforces it.
package channel
