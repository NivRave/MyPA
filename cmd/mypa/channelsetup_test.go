package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/twilio"
	"github.com/stretchr/testify/assert"
)

func TestChannelSetup_CommandLine(t *testing.T) {
	sandbox := adapters.Config{WhatsApp: twilio.Config{AccountSID: "AC1", AuthToken: "tok", FromNumber: twilio.SandboxSender, SandboxJoin: "join plenty-tiger"}}
	run := func(cfg adapters.Config, args ...string) (int, string) {
		var out bytes.Buffer
		code := runChannelSetup(context.Background(), &out, cfg, args)
		return code, out.String()
	}

	code, out := run(sandbox)
	assert.Equal(t, 2, code)
	assert.Contains(t, out, "usage: mypa channel-setup")
	assert.Contains(t, out, twilio.Name, "the usage lists the channels that have a guide")

	code, _ = run(sandbox, "whatsapp", "telegram")
	assert.Equal(t, 2, code)
	code, _ = run(sandbox, "--force", "whatsapp")
	assert.Equal(t, 2, code)

	code, out = run(sandbox, "telegram")
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "no guided setup")

	code, out = run(sandbox, "whatsapp")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "`join plenty-tiger`")

	code, out = run(sandbox, "whatsapp", "--submit")
	assert.Equal(t, 1, code, "a sandbox has no templates to submit")
	assert.Contains(t, out, "registered sender")
}
