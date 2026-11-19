package channel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var yesNo = []Choice{
	{ID: "approve", Label: "Approve", TypedReply: "CONFIRM DELETE_TASK"},
	{ID: "reject", Label: "Reject", TypedReply: "Reject"},
}

func TestRender_ChoicesBecomeANumberedListWithoutButtons(t *testing.T) {
	out := Render(validCaps(), Outbound{Text: "Delete the task?", Choices: yesNo})
	assert.Nil(t, out.Choices)
	assert.Equal(t, "Delete the task?\n\n1. Approve (reply CONFIRM DELETE_TASK)\n2. Reject", out.Text)
}

func TestRender_ChoicesStayButtonsWhenTheyFit(t *testing.T) {
	caps := validCaps()
	caps.MaxChoices, caps.MaxChoiceLabel = 3, 20
	out := Render(caps, Outbound{Text: "Delete the task?", Choices: yesNo})
	assert.Equal(t, yesNo, out.Choices)
	assert.Equal(t, "Delete the task?", out.Text)
}

func TestRender_TooManyChoicesAreAllListedNeverMixed(t *testing.T) {
	caps := validCaps()
	caps.MaxChoices, caps.MaxChoiceLabel = 1, 20
	out := Render(caps, Outbound{Text: "Pick", Choices: yesNo})
	assert.Nil(t, out.Choices, "more choices than buttons: no buttons at all")
	assert.Contains(t, out.Text, "1. Approve")
	assert.Contains(t, out.Text, "2. Reject")

	caps.MaxChoices, caps.MaxChoiceLabel = 3, 3
	out = Render(caps, Outbound{Text: "Pick", Choices: yesNo})
	assert.Nil(t, out.Choices, "a label over the limit: the list for all of them")
}

func TestRender_UnsendableAttachmentsBecomeANote(t *testing.T) {
	caps := validCaps()
	caps.AttachmentsOut = []AttachmentKind{KindImage}
	in := Outbound{Text: "Here you go", Attachments: []Attachment{
		{Kind: KindImage, Name: "chart.png", Data: []byte{1}},
		{Kind: KindDocument, Name: "report.pdf", Data: []byte{2}},
		{Kind: KindAudio, ContentType: "audio/ogg", Data: []byte{3}},
	}}
	out := Render(caps, in)
	assert.Len(t, out.Attachments, 1)
	assert.Equal(t, "chart.png", out.Attachments[0].Name)
	assert.Equal(t, "Here you go\n\n(The file \"report.pdf\" could not be sent here.)\n\n(The file \"audio/ogg\" could not be sent here.)", out.Text)
	assert.Len(t, in.Attachments, 3, "the caller's message is not modified")
}

func TestRender_VoiceFallsBackToText(t *testing.T) {
	m := Outbound{Text: "It is sunny.", Voice: &Audio{ContentType: "audio/ogg", Data: []byte{1}}}
	out := Render(validCaps(), m)
	assert.Nil(t, out.Voice)
	assert.Equal(t, "It is sunny.", out.Text)

	caps := validCaps()
	caps.VoiceOut = true
	assert.NotNil(t, Render(caps, m).Voice)
}

func TestRender_PlainTextIsUntouched(t *testing.T) {
	m := Outbound{Text: "**bold** and a long line", ReplyContext: []byte("ctx")}
	assert.Equal(t, m, Render(validCaps(), m))
}
