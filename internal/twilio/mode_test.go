package twilio

import (
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/deploy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envOf(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

// configured is a WhatsApp setup with the given extra settings.
func configured(extra map[string]string) Config {
	kv := map[string]string{"TWILIO_ACCOUNT_SID": "AC1", "TWILIO_AUTH_TOKEN": "tok"}
	for k, v := range extra {
		kv[k] = v
	}
	return LoadConfig(envOf(kv), deploy.Load(envOf(nil)))
}

// TestMode_SwitchesByConfigurationAlone is the configuration half of V4-41's "done when": the
// same install is a sandbox or a registered sender by its settings, and nothing else.
func TestMode_SwitchesByConfigurationAlone(t *testing.T) {
	sandbox := configured(map[string]string{"TWILIO_WHATSAPP_MODE": "sandbox", "TWILIO_FROM_NUMBER": SandboxSender, "TWILIO_SANDBOX_JOIN_CODE": "join plenty-tiger"})
	sender := configured(map[string]string{"TWILIO_WHATSAPP_MODE": "sender", "TWILIO_FROM_NUMBER": "whatsapp:+12025550199"})

	require.Empty(t, sandbox.Validate(true, true))
	require.Empty(t, sender.Validate(true, true))
	assert.Equal(t, string(ModeSandbox), NewAdapter(sandbox).Mode())
	assert.Equal(t, string(ModeSender), NewAdapter(sender).Mode())
	assert.NotEqual(t, NewAdapter(sandbox).Capabilities().Proactive, NewAdapter(sender).Capabilities().Proactive,
		"the two modes accept different unprompted messages")
	assert.NotEmpty(t, NewAdapter(sandbox).JoinStep(), "a sandbox needs a join message first")
	assert.Empty(t, NewAdapter(sender).JoinStep(), "a registered sender needs nothing before the first message")
	assert.Equal(t, []Mode{ModeSandbox, ModeSender}, Modes())
}

// TestMode_UnsetFollowsTheNumber: an install upgraded without the new setting keeps working as it
// did. The sandbox number means sandbox; any other number is a registered sender.
func TestMode_UnsetFollowsTheNumber(t *testing.T) {
	assert.Equal(t, ModeSandbox, configured(map[string]string{"TWILIO_FROM_NUMBER": SandboxSender}).Mode)
	assert.Equal(t, ModeSandbox, configured(map[string]string{"TWILIO_FROM_NUMBER": " WhatsApp:+14155238886 "}).Mode)
	assert.Equal(t, ModeSender, configured(map[string]string{"TWILIO_FROM_NUMBER": "whatsapp:+12025550199"}).Mode)
	assert.Equal(t, ModeSandbox, configured(map[string]string{"TWILIO_WHATSAPP_MODE": " Sandbox "}).Mode, "case and spaces do not matter")
	assert.Equal(t, ModeSandbox, Config{}.Effective().Mode, "an unconfigured adapter reads as the sandbox number would")
}

func TestMode_ValidateRefusesWhatCannotWork(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]string
		want  string
	}{
		{"unknown mode", map[string]string{"TWILIO_WHATSAPP_MODE": "business"}, "TWILIO_WHATSAPP_MODE"},
		{"sender on the sandbox number", map[string]string{"TWILIO_WHATSAPP_MODE": "sender", "TWILIO_FROM_NUMBER": SandboxSender}, "sandbox number"},
		{"template that is not a content SID", map[string]string{"TWILIO_TEMPLATE_REMINDER_SID": "reminder"}, "TWILIO_TEMPLATE_REMINDER_SID"},
		{"briefing template that is not a content SID", map[string]string{"TWILIO_TEMPLATE_BRIEFING_SID": "HX123"}, "TWILIO_TEMPLATE_BRIEFING_SID"},
		{"join code over several lines", map[string]string{"TWILIO_SANDBOX_JOIN_CODE": "join a\nb"}, "TWILIO_SANDBOX_JOIN_CODE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := configured(tc.extra).Validate(false, true)
			require.Len(t, errs, 1)
			assert.Contains(t, errs[0].Error(), tc.want)
			assert.NotContains(t, errs[0].Error(), "tok", "no secret in the message")
		})
	}
	t.Run("an unconfigured adapter is never refused for its mode", func(t *testing.T) {
		assert.Empty(t, LoadConfig(envOf(map[string]string{"TWILIO_WHATSAPP_MODE": "business"}), deploy.Load(envOf(nil))).Validate(true, true))
	})
}

func TestMode_JoinCodeIsWrittenAsTheMessageToSend(t *testing.T) {
	for _, raw := range []string{"plenty-tiger", "join plenty-tiger", "  JOIN plenty-tiger "} {
		assert.Equal(t, "join plenty-tiger", configured(map[string]string{"TWILIO_SANDBOX_JOIN_CODE": raw}).SandboxJoin, raw)
	}
	step := NewAdapter(configured(map[string]string{"TWILIO_FROM_NUMBER": SandboxSender, "TWILIO_SANDBOX_JOIN_CODE": "plenty-tiger"})).JoinStep()
	assert.Contains(t, step, "join plenty-tiger")
	assert.Contains(t, step, "+14155238886")

	unset := NewAdapter(configured(map[string]string{"TWILIO_FROM_NUMBER": SandboxSender})).JoinStep()
	assert.Contains(t, unset, "+14155238886")
	assert.Contains(t, unset, "Twilio Console", "without the code the step says where to find it")
	assert.False(t, strings.Contains(unset, "join plenty"), unset)
}

func TestTemplates_WordingWhatsAppAccepts(t *testing.T) {
	tpls := Templates()
	require.Len(t, tpls, 2)
	keys := map[string]bool{}
	for _, tp := range tpls {
		keys[tp.Key] = true
		assert.Regexp(t, `^[a-z0-9_]+$`, tp.Name, "WhatsApp template names are lower case, digits and underscores")
		assert.Equal(t, "UTILITY", tp.Category)
		assert.NotEmpty(t, tp.Language)
		assert.Contains(t, tp.Body, "{{1}}")
		assert.False(t, strings.HasPrefix(strings.TrimSpace(tp.Body), "{{"), "WhatsApp rejects a body that starts with a variable")
		assert.False(t, strings.HasSuffix(strings.TrimSpace(tp.Body), "}}"), "WhatsApp rejects a body that ends with a variable")
		assert.Len(t, tp.Samples, strings.Count(tp.Body, "{{"), "one sample per variable, which approval needs")
		assert.LessOrEqual(t, len(tp.Body), 1024)
	}
	assert.Equal(t, map[string]bool{TemplateReminder: true, TemplateBriefing: true}, keys)
	assert.Equal(t, "HX0123456789abcdef0123456789abcdef", configured(map[string]string{"TWILIO_TEMPLATE_REMINDER_SID": "HX0123456789abcdef0123456789abcdef"}).TemplateSIDs[TemplateReminder])
}
