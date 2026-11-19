// Package conformance holds the channel conformance suite (ROADMAP V4-34).
//
// One shared set of scenarios (pairing, approval, reminder, undo, media, long reply, plus a plain
// reply and a duplicate delivery) is run against every channel MyPA supports. Each scenario drives
// the real channel adapter (its webhook route, with the platform's own authentication), the real
// engine and the real platform API client. Only the network is replaced: a fake transport answers
// as the platform's API would and records every outbound request. Nothing in the suite can reach a
// real platform.
//
// What a scenario asserts comes in two layers:
//
//   - the outcome, in channel-neutral terms (the tool ran once, the reminder is marked sent);
//   - the bytes on the wire, as a golden transcript per scenario and channel in testdata/golden.
//
// The transcripts pin today's behaviour. A change to one is a change in what users receive and
// must be deliberate: run `go test ./internal/conformance -update` and review the diff.
//
// Stage 1 of V4.0 pinned the two existing channels through their entry points of the time. Since
// the channel layer (V4-31) the scenarios drive the adapters the app registers, through the
// production registry (internal/channel/adapters), and the transcripts stayed byte-identical
// through that port. Tying registration to passing the suite completes V4-34.
//
// The package has no production code: everything lives in its tests.
package conformance
