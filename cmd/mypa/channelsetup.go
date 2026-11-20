package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/config"
)

// channelSetupUsage is printed for a wrong command line.
func channelSetupUsage() string {
	return "usage: mypa channel-setup <channel> [--submit]\n" +
		"  Shows how to set up the channel in the mode its settings choose.\n" +
		"  --submit  also make the changes the guide describes in the platform account (for example, create and submit message templates).\n" +
		"  Channels with a guided setup: " + strings.Join(adapters.SetupGuides(), ", ") + "\n"
}

// channelSetupMain is `mypa channel-setup <channel> [--submit]` (V4-41): the guided setup of one
// channel, run by the owner next to the app (`docker compose run --rm app channel-setup ...`).
// It reads the same settings as the app and never edits them.
func channelSetupMain(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stdout, "✗ cannot load the configuration: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return runChannelSetup(ctx, os.Stdout, cfg.Channels, args)
}

// runChannelSetup parses the arguments and runs the guide, reporting to out.
func runChannelSetup(ctx context.Context, out io.Writer, cfg adapters.Config, args []string) int {
	var name string
	submit := false
	for _, a := range args {
		switch {
		case a == "--submit":
			submit = true
		case strings.HasPrefix(a, "-") || name != "":
			fmt.Fprint(out, channelSetupUsage())
			return 2
		default:
			name = a
		}
	}
	if name == "" {
		fmt.Fprint(out, channelSetupUsage())
		return 2
	}
	if err := adapters.Setup(ctx, cfg, name, out, submit, nil); err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	return 0
}
