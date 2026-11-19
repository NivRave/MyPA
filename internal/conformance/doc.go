// Package conformance holds the channel conformance suite (ROADMAP V4-34).
//
// One shared set of scenarios (pairing, approval, reminder, undo, media, long reply, plus a plain
// reply and a duplicate delivery) is run against every channel MyPA supports. Each scenario drives
// the real ingress code of the platform (the Telegram update parser, the signed Twilio webhook
// handler), the real engine and the real platform API client. Only the network is replaced: a fake
// transport answers as the platform's API would and records every outbound request. Nothing in
// the suite can reach a real platform.
//
// What a scenario asserts comes in two layers:
//
//   - the outcome, in channel-neutral terms (the tool ran once, the reminder is marked sent);
//   - the bytes on the wire, as a golden transcript per scenario and channel in testdata/golden.
//
// The transcripts pin today's behaviour. A change to one is a change in what users receive and
// must be deliberate: run `go test ./internal/conformance -update` and review the diff.
//
// Stage 1 of V4.0 pins the two existing channels through their current entry points. When the
// channel layer exists (V4-31) the same scenarios run against every registered adapter, and an
// adapter cannot be registered without passing them.
//
// The package has no production code: everything lives in its tests.
package conformance
