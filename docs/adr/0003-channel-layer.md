# ADR 0003 — Channel layer: adapters, capabilities, proactive delivery, groups

- **Status:** Accepted (2026-11-19). The owner's decisions are in §11. Not yet implemented.
- **Date:** 2026-11-19
- **Context owner:** MyPA maintainers
- **Needed before:** V4-31, V4-33, V4-16. Fixed by [`ROADMAP.md`](../ROADMAP.md) and not reopened: WhatsApp stays on Twilio in both modes and has no group chat at first (DE-8); every flow can be completed with typed replies (principle 8). Identity is ADR 0002.

## 1. Context: the code as it is (`dev` at `3807fb9`)
- **Inbound.** Both webhooks produce one `models.Message` that carries Telegram-only fields and a `Source` string (`internal/models/models.go:10-39`). Telegram reads `message` updates only, so a button press could not arrive, and it keeps no chat type, so a group looks like a private chat (`internal/telegram/handler.go:12-15`, `:51-54`, `:92-101`). A Telegram document is dropped (`:73-79`, BL-18). The sender is verified at the edge and both checks fail closed (`cmd/gateway/main.go:134-139`, `:184-189`; `internal/twilio/handler.go:98-111`).
- **Outbound** is text only. The engine branches on the platform name to choose a client, a markup and a split length (`internal/orchestrator/engine.go:251-267`; 4,000 for Telegram, 1,500 in `internal/twilio/client.go:52-54`); media handling branches again (`internal/orchestrator/media.go:60`). The partner notifier guesses the channel from a leading `+` (`cmd/orchestrator/main.go:216-226`).
- **Proactive.** Reminders, broadcasts and scheduled rules go to the chat of the person's latest audit session (`internal/orchestrator/engine.go:434-450`, `:475-490`; `internal/scheduler/scheduler.go:192-208`), so someone who never wrote gets nothing, and someone who last wrote in a group would be reminded there. A reminder is marked sent as soon as the platform accepts the request (`internal/orchestrator/engine.go:496-504`; `internal/twilio/client.go:93-98`). When WhatsApp later refuses it outside the 24-hour window, the report is only logged (`internal/twilio/handler.go:117`, `:145-152`): the reminder is lost. The only trace of when a person last wrote on a channel is their audit sessions (`internal/models/models.go:82-87`), and nothing reads it for this.
- **Public address.** Telegram is webhook-only (`cmd/gateway/main.go:81-92`); four settings must agree on one host (`.env.example:40`, `:89`, `:104`, `:116`).
- **Conformance (V4-34).** Twelve scenarios run on every entry of a `channels` list kept in the test (`internal/conformance/scenarios_test.go:25-38`, `internal/conformance/harness_test.go:80-104`), against the real parsers and clients over a fake transport, with golden transcripts. Nothing ties that list to what the app actually starts.

## 2. Decision 1 — The adapter interface and the neutral messages
| | Option | Pros | Cons |
| --- | --- | --- | --- |
| 1a | One interface; capabilities declared as data; one shared renderer applies the fallbacks before the adapter is called | An adapter never receives what it cannot send; each fallback is written and tested once; the matrix can be generated | The shared renderer must know every capability |
| 1b | A small core interface plus optional interfaces found by type assertion | Idiomatic Go | "Up to 3 buttons" or "1,500 bytes" is not a type; every caller repeats the fallback |
| 1c | Text only for every channel | Trivial | No buttons, attachments or voice: fails V4-10 and V4-18 |

**Challenge.** 1a puts the cleverness in one renderer that can become a platform switch in disguise. The guard is the V4-31 test that no code outside an adapter names a platform; the renderer may read declared values only.

**Recommendation: 1a.**
```go
type Adapter interface {
    Name() string                                     // the only place a platform is named
    Capabilities() Capabilities                       // of this configured instance (WhatsApp sandbox and sender differ)
    Ingress() Ingress                                 // Webhook{Routes} or Outward{Run(ctx, sink)}; see Decision 5
    Send(ctx context.Context, to Address, m Outbound) (Receipt, error)
    Fetch(ctx context.Context, a AttachmentRef) (io.ReadCloser, error)
}
type Inbound struct {
    Channel, MessageID string       // MessageID is unique within Conversation; the duplicate claim uses both
    Sender       ExternalID         // verified by the adapter, never taken from message text
    Conversation Conversation       // ID, Kind (Direct or Group), Addressed (mention, reply or command)
    Text, ChoiceID string           // ChoiceID is set when a button was pressed
    Attachments  []AttachmentRef    // Kind (image, audio, document), ContentType, Size
    ReplyContext []byte             // opaque to the engine; handed back in Outbound
}
type Outbound struct {
    Text         string             // the Markdown subset the model writes today; the adapter converts it
    Choices      []Choice           // ID (at most 64 bytes), Label, TypedReply: every choice can also be typed
    Attachments  []Attachment
    Voice        *Audio
    Proactive    *Proactive         // nil for a reply; otherwise Category and the template key and values
    ReplyContext []byte
}
```
`Receipt` is accepted or refused with a reason (window closed, not reachable, rate limited with a retry time, too large). Delivery reports that arrive later use the same reasons. An adapter acknowledges a webhook or button press itself, at once; the turn follows through the queue.

## 3. Decision 2 — Capabilities and the fallback for each missing one
**Options.** *2a:* each adapter degrades as it sees fit. *2b:* one documented fallback per capability, applied by the shared renderer. **Challenge:** 2b cannot use a platform's own alternative, such as a WhatsApp list message. Accepted for now: one behaviour per gap is what makes "degrades in a documented way" testable, and an adapter can later declare a richer capability. **Recommendation: 2b.**

| Declared capability | Telegram | WhatsApp (Twilio) | When missing or exceeded |
| --- | --- | --- | --- |
| `MaxChoices`, `MaxChoiceLabel` | Inline buttons | 3 quick replies, inside the window only, as content templates (V4-10) | A numbered list in the text; the person replies with the number or the words. More choices than the limit: all of them as the list, never a mix |
| `InviteLink` | Deep link that sends `/start <code>` | Link that pre-fills the code (sandbox: after the join step) | The typed code |
| `Groups` | Yes | No (DE-8) | Direct chats; the request is answered with where groups are available |
| `VoiceOut` | Yes | Yes, inside the window | Text |
| `AttachmentsIn`, `AttachmentsOut` | Photo, voice (documents: BL-18) | Image, PDF, text file, audio | A reply naming the types that work, as today (`internal/orchestrator/media.go:120`); outbound: a link, or text saying it could not be sent |
| `MaxTextLen`, `Markup` | 4,000 bytes, HTML | 1,500 bytes, WhatsApp markup | Today's values (`internal/orchestrator/engine.go:260`, `internal/twilio/client.go:54`), below the platforms' 4,096 and 1,600, and counted in bytes as today. The port keeps today's converters and split functions unchanged: Telegram splits at a paragraph, line, sentence or space and keeps tags balanced (`internal/markdown/telegram.go:148`); WhatsApp at a paragraph, line or space (`internal/twilio/client.go:103`) |
| `Proactive` | Always | 24-hour window; templates in sender mode only | Decision 3 |

## 4. Decision 3 — Proactive delivery
| | Option | Pros | Cons |
| --- | --- | --- | --- |
| 3a | Send, and react to the refusal | Least code | Depends on a callback that may be unset; the reminder is already marked sent |
| 3b | Decide before sending from `last_inbound_at` and the adapter's rule; every message is a row with a state; a later refusal reopens it | Nothing is marked sent on hope; the outcome is recorded (V4-33) | A new table and a small state machine |
| 3c | Ask people to write daily to keep the window open | None | Not a product |

**Challenge.** 3b predicts a window it cannot see; Twilio's clock is the truth. So a refusal report still re-enters the same chain, and 3b needs the status callback to be configured: `/doctor` already warns when it is not (`internal/doctor/checks.go:342-347`).

**Recommendation: 3b.** Route order: the preferred channel if open; else an approved template on it; else the person's other channel; else hold until they next write, then deliver with a note that it is late. Sender mode: MyPA supplies two templates (V4-41), one for a reminder carrying its title as a single short line, one telling the person a briefing is waiting. Sandbox mode has no templates of ours, so the chain skips that step; a lapsed sandbox membership counts as not reachable and the person is told how to rejoin on their other channel. Each message ends as delivered, delivered late, moved to another channel, or expired, and `/doctor` counts what is held.

## 5. Decision 4 — Group chats
| | Option | Pros | Cons |
| --- | --- | --- | --- |
| 4a | The assistant sits in a group bound to one household and acts only when addressed | What V4-16 asks for; low cost | Needs rules for strangers and for private data |
| 4b | No groups: one-to-one chats, with partner notifications as the shared view | Works on every channel; exists today (`internal/domains/shared/notifier.go:126-149`) | Not a conversation |
| 4c | The assistant reads every group message | Most "present" | Cost on every message (principle 5) and a privacy problem |

**Challenge.** 4a shows answers to everyone in the group, members or not. So the group is a narrower audience than any of its members, never a wider one. **Recommendation: 4a where `Groups` is declared, with 4b as the documented fallback elsewhere, WhatsApp included.** Rules, enforced by the layer and not by adapters: an owner binds a group to their household with a command in that group, and unbound groups are ignored; each message is authorised by its sender; a sender who is not a member of that household gets no reply at all, not even the pairing prompt; only data shared with the household can be read or written, never personal data; codes, links and sensitive confirmations are refused in a group and the person is sent to the direct chat; proactive messages go to the direct chat, never to a group.

**History, ordering and approvals.** Until V4-32, nothing changes: one processing lane per channel and sender (`internal/broker/consumer.go:150`), and history and approvals keyed by the platform ID. From V4-32: one processing lane per person, whichever channel or conversation a message comes from; direct-chat history belongs to the person and is shared across their channels; group history belongs to the conversation; a pending approval belongs to the person and to the conversation it was raised in, and can be confirmed only there.

## 6. Decision 5 — Public address, and registering with the conformance suite
- **Public address. Options:** a boolean on the adapter, or derive it from how the adapter receives. **Recommendation:** derive it. `Ingress()` returns `Webhook` (needs one) or `Outward` (polling or a socket; needs none). V4-31 defines the interface with both kinds and ports Telegram and WhatsApp as `Webhook`, as they are today. Telegram polling, an `Outward` ingress, is built in V4-40; from then an adapter with two modes reports the one in use. At start, an enabled webhook adapter with no public address configured stops the app with a message naming it; `/doctor` shows each adapter's mode. **Challenge:** polling is a second code path per platform. It is what lets a Telegram-only install run with no tunnel, which is V4-40's "done when", so it is built there.
- **Conformance. Options:** keep a list in the test, or tie the suite to a production registry. **Recommendation:** adapters are constructed only through `channel.Register`. The suite reads the registry; a test fails when a registered adapter has no fixture (how to forge its inbound payloads and fake its API) or a fixture has no adapter. Every scenario runs on every adapter; where a capability is missing the scenario asserts the documented fallback and is never skipped. The matrix in `SYSTEM.md` is generated from `Capabilities()` and a test fails when it is stale. WhatsApp is registered once per mode. **Challenge:** a fixture is real work for each new channel. That is the intended price of V4-34.

## 7. Paper check: Discord as a third adapter
From Discord's published API as known on 2026-11-19; not re-read for this ADR, so confirm when building V4-39.
- **Fits.** A bot connects outward over a socket and receives button presses on it: `Outward` ingress, no public address. Stable user IDs. 2,000-character messages and Markdown. Up to 25 buttons with an ID of up to 100 characters. Direct messages and mentions arrive without the privileged message-content permission, which matches addressed-only groups. A refused direct message maps to "not reachable".
- **Fits only because of a field added for it.** A button press must be acknowledged within about three seconds and answered through a token that expires after about 15 minutes, while a turn may take five (`internal/orchestrator/engine.go:224`). Hence the acknowledgement at ingress and the opaque `ReplyContext`; Telegram buttons need the same.
- **Does not fit.** (1) A server contains many channels and threads, and `Conversation` has no parent, so a household can be bound per channel but not per server. (2) Native slash commands must be registered with Discord up front, and the interface carries no command list. (3) There is no link that both opens the bot and carries a code, and a stranger cannot message a bot they share no server with, so onboarding has a "how to reach the bot" step no other channel has. (4) A voice note is an audio attachment with extra metadata, so `VoiceOut` may arrive as a file. (5) Exactly one process may hold the socket, so with split roles (V4-01) it belongs to the ingress role.

## 8. Consequences
- `models.Message` gives way to `Inbound`; the queue payload changes, so the queue is drained before the release that changes it, as the upgrade procedure already requires (V4-43).
- V4-31 lands before V4-32, so until then `Sender` is still matched against `users.platform_id`.
- New tables: the proactive delivery rows and the group bindings. `SYSTEM.md` §3.7 becomes the generated matrix. The golden transcripts stay byte-identical through the port; that is the proof the port changed nothing, and why it keeps today's split lengths and functions (§3).

## 9. Not decided here
- Editing or deleting sent messages, typing indicators and reactions.
- Household-level messages addressed to a group, such as a shared morning briefing.
- WhatsApp list messages, and the items under "Does not fit" in §7: decided with V4-10 and V4-39.
- The exact template wording (V4-41) and retry timing for rate limits.

## 10. Questions for the owner
1. **Outside the WhatsApp window, try a template before the other channel?** Recommended: yes. The person chose that channel. It costs a template fee per message in sender mode; reversing the order is cheaper and reaches them where they may not look.
2. **What may a template carry?** Recommended: the reminder's title, one short line; nothing for a briefing beyond "it is waiting". The full text follows once they reply.
3. **How long is an undeliverable message held?** Recommended: a reminder for 7 days, delivered marked late; a briefing until the end of its day.
4. **In a group, only data shared with the household?** Recommended: yes, with no exception for the person asking.
5. **Build Telegram polling with the adapter port (V4-31) or with V4-40?** The draft recommended V4-31. Revised: V4-31 defines the `Ingress` interface, so polling fits without reopening the adapter; polling itself is built in V4-40, where its "no tunnel" test is.

## 11. Decision record (2026-11-19)

Sections 1 to 10 are the draft of 2026-11-19 after an independent review. This section records what the owner decided.

- **Questions 1 to 4: as recommended.** Outside the WhatsApp window a template is tried before the other channel; a template carries a reminder's title as one short line and, for a briefing, only that it is waiting; an undeliverable reminder is held 7 days and delivered marked late, a briefing until the end of its day; in a group only data shared with the household is used, with no exception for the person asking.
- **Question 5:** V4-31 defines the `Ingress` interface; Telegram polling is built in V4-40 (§6).
- **History, ordering and approvals** follow §5, in the same words as ADR 0002 §9.
- **Message length:** the declared limits are today's 4,000 and 1,500 bytes, and the port keeps today's split functions (§3).
