package channel

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatrix_RendersEveryCapabilityPerAdapterInOrder(t *testing.T) {
	plain := newFake("plain")
	rich := newFake("rich-chat")
	rich.caps = Capabilities{
		MaxChoices: 3, MaxChoiceLabel: 20, InviteLink: true, Groups: true, VoiceOut: true,
		AttachmentsIn:  []AttachmentKind{KindImage, KindDocument},
		AttachmentsOut: []AttachmentKind{KindAudio},
		MaxTextLen:     12345, Markup: HTML, Proactive: "within a day | or a template",
	}
	rich.ingress = Ingress{Outward: &Outward{Run: func(context.Context, Sink) error { return nil }}}

	got := Matrix([]Adapter{plain, rich})
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	require.Len(t, lines, 2+len(matrixRows), "a header, a separator and one line per capability")
	assert.Equal(t, "| Capability | `plain` | `rich-chat` | When missing |", lines[0])
	assert.Equal(t, "| :--- | :--- | :--- | :--- |", lines[1])

	row := func(prefix string) string {
		t.Helper()
		for _, l := range lines {
			if strings.HasPrefix(l, "| "+prefix) {
				return l
			}
		}
		t.Fatalf("no row %q in:\n%s", prefix, got)
		return ""
	}
	assert.Contains(t, row("Receives messages"), "| webhook (needs a public address) | outward connection (no public address) |")
	assert.Contains(t, row("Buttons"), "| none | up to 3, labels up to 20 bytes |")
	assert.Contains(t, row("Invite link"), "| no | yes |")
	assert.Contains(t, row("Group chats"), "| no | yes |")
	assert.Contains(t, row("Voice replies"), "| no | yes |")
	assert.Contains(t, row("Files received"), "| none | image, document |")
	assert.Contains(t, row("Files sent"), "| none | audio |")
	assert.Contains(t, row("Longest message"), "| 1,000 bytes | 12,345 bytes |")
	assert.Contains(t, row("Formatting"), "| plain text | HTML |")
	assert.Contains(t, row("Unprompted messages"), `| always | within a day \| or a template |`, "a pipe in a declared value is escaped")
	assert.Contains(t, row("Buttons"), "numbered list", "each row names its documented fallback")
}

func TestMatrix_HasNoPlaceholderCells(t *testing.T) {
	for _, l := range strings.Split(Matrix([]Adapter{newFake("chat")}), "\n") {
		assert.NotContains(t, l, "||", "an empty cell in %q", l)
	}
	assert.Equal(t, "", Matrix(nil), "no adapters, no matrix")
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 1500: "1,500", 4000: "4,000", 1234567: "1,234,567"} {
		assert.Equal(t, want, thousands(n))
	}
}
