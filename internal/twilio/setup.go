package twilio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SetupCommand is how the owner runs the guided setup (cmd/mypa `channel-setup`).
const SetupCommand = "mypa channel-setup " + Name

// Guide writes the setup steps for the configured mode (V4-41). In sender mode it shows MyPA's
// templates; with submit it creates each one that has no SID set yet in the owner's Twilio
// account, asks WhatsApp to approve it, and prints the setting to add. Nothing is changed in
// Twilio without submit, and the guide never edits the settings itself.
func Guide(ctx context.Context, cfg Config, api ContentAPI, w io.Writer, submit bool) error {
	cfg = cfg.Effective()
	if cfg.AccountSID == "" {
		return errors.New("WhatsApp is not configured: set TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN and TWILIO_FROM_NUMBER first")
	}
	if cfg.Mode == ModeSandbox {
		if submit {
			return errors.New("templates are for a registered sender: the sandbox has only Twilio's own. Set TWILIO_WHATSAPP_MODE=sender with your registered number first")
		}
		guideSandbox(cfg, w)
		return nil
	}
	return guideSender(ctx, cfg, api, w, submit)
}

func guideSandbox(cfg Config, w io.Writer) {
	number := strings.TrimPrefix(SandboxSender, "whatsapp:")
	join := "not set"
	if cfg.SandboxJoin != "" {
		join = "`" + cfg.SandboxJoin + "`"
	}
	fmt.Fprintf(w, "WhatsApp runs in sandbox mode, on Twilio's shared number %s.\n\n", number)
	fmt.Fprintln(w, "1. In the Twilio Console open Messaging → Try it out → Send a WhatsApp message and note the join code (two words joined by a dash).")
	fmt.Fprintf(w, "2. Set TWILIO_SANDBOX_JOIN_CODE to it and restart the app (now: %s).\n", join)
	fmt.Fprintf(w, "3. Before their first message, each person sends the join message to %s on WhatsApp. Invitations and /link codes say so.\n", number)
	fmt.Fprintln(w, "4. Twilio drops a person 72 hours after they last wrote, and messages to them then fail. /doctor names who, with the message to send again.")
	fmt.Fprintln(w, "\nThe sandbox has no templates of MyPA's, so a reminder due more than 24 hours after someone last wrote cannot go out on WhatsApp.")
	fmt.Fprintf(w, "To remove these limits, register a WhatsApp sender in your Twilio account, set TWILIO_FROM_NUMBER to it and TWILIO_WHATSAPP_MODE=sender, restart, and run %s again.\n", SetupCommand)
}

func guideSender(ctx context.Context, cfg Config, api ContentAPI, w io.Writer, submit bool) error {
	fmt.Fprintf(w, "WhatsApp runs with your registered sender %s.\n\n", strings.TrimPrefix(cfg.FromNumber, "whatsapp:"))
	fmt.Fprintln(w, "Outside the 24-hour window after a person last wrote, WhatsApp accepts only templates it approved. MyPA uses two,")
	fmt.Fprintln(w, "approved in your own Twilio account. Their wording:")
	for _, tp := range Templates() {
		state := "not set"
		if sid := cfg.TemplateSIDs[tp.Key]; sid != "" {
			state = "set (" + sid + "); /doctor shows its approval"
		}
		fmt.Fprintf(w, "\n• %s (category %s, language %s). For: %s\n  Text: %s\n  Example for {{1}}: %s\n  %s: %s\n",
			tp.Name, tp.Category, tp.Language, tp.Why, tp.Body, strings.Join(tp.Samples, ", "), templateEnvKeys[tp.Key], state)
	}
	if !submit {
		fmt.Fprintf(w, "\nTo create and submit the missing ones from here, run %s --submit.\n", SetupCommand)
		fmt.Fprintln(w, "Or create them in the Twilio Console (Messaging → Content Template Builder) with exactly this wording, submit them for WhatsApp, and set their Content SIDs.")
		return nil
	}
	if api == nil {
		return errors.New("no Twilio client")
	}
	fmt.Fprintln(w)
	var lines []string
	for _, tp := range Templates() {
		if cfg.TemplateSIDs[tp.Key] != "" {
			fmt.Fprintf(w, "%s: already set, not created again.\n", tp.Name)
			continue
		}
		sid, err := api.CreateTemplate(ctx, tp)
		if err != nil {
			return fmt.Errorf("creating %s: %w", tp.Name, err)
		}
		if err := api.SubmitTemplate(ctx, sid, tp); err != nil {
			return fmt.Errorf("%s was created as %s but not submitted: %w (submit it in the Twilio Console, then set %s=%s)",
				tp.Name, sid, err, templateEnvKeys[tp.Key], sid)
		}
		fmt.Fprintf(w, "%s: created as %s and submitted for WhatsApp's approval.\n", tp.Name, sid)
		lines = append(lines, templateEnvKeys[tp.Key]+"="+sid)
	}
	if len(lines) > 0 {
		fmt.Fprintln(w, "\nAdd these lines to .env and restart the app; /doctor then shows the approval of each:")
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
	}
	return nil
}
