package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/reencrypt"
	"github.com/nivik/mypa/internal/state"
)

// `mypa reencrypt [--dry-run]`: the re-encryption pass (V4-07, BL-16). A one-shot process, like
// `transfer` and `embeddings`. It is safe while the app runs: a value is replaced only while it
// still holds what was read.
const reencryptUsage = `Usage: mypa reencrypt [--dry-run]

Rewrites every value still in the old, unsalted encryption format in the current format, in the
three places that hold encrypted values: the Google sign-ins in Redis, the credential fallback
table and the API connector headers. Then reports, for each, how many old-format values remain.
Run it with the ENCRYPTION_KEY the values were written with.

  --dry-run   only count; change nothing

Run it where the app runs, for example:

  docker compose run --rm app reencrypt --dry-run
  docker compose run --rm app reencrypt

When every place reports 0 old-format values, the old format is no longer needed (V4-45).
`

// reencryptDeps is everything the command talks to. Production builds it from the environment.
type reencryptDeps struct {
	Stores []reencrypt.Store
	Out    io.Writer
}

func reencryptMain(args []string) int {
	out := os.Stdout
	for _, a := range args {
		if a == "help" || a == "-h" || a == "--help" {
			fmt.Fprint(out, reencryptUsage)
			return 2
		}
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(out, "✗ cannot load the configuration: %v\n", err)
		return 1
	}
	if err := crypto.ValidateKeyConfigured(); err != nil {
		fmt.Fprintf(out, "✗ %v: the pass needs the key the values were written with\n", err)
		return 1
	}
	client, err := db.Open(cfg.Database.URL)
	if err != nil {
		fmt.Fprintf(out, "✗ cannot connect to the database: %v\n", err)
		return 1
	}
	defer func() { _ = client.Close() }()
	sqlDB, err := client.DB.DB()
	if err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := db.EnsureMigrated(ctx, sqlDB); err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	cache, err := state.NewStore(cfg.Redis.URL)
	if err != nil {
		fmt.Fprintf(out, "✗ cannot connect to Redis, which holds the Google sign-ins: %v\n", err)
		return 1
	}
	defer func() { _ = cache.Close() }()
	return runReencrypt(ctx, args, reencryptDeps{
		Stores: []reencrypt.Store{cache.TokenStore(), db.CredentialFallbackStore(sqlDB), db.ConnectorHeadersStore(sqlDB)},
		Out:    out,
	})
}

// runReencrypt runs the pass and prints its report. It returns 0 when the pass ran (a dry run, or
// a run that left no old-format value), 1 when it failed or left old-format values, and 2 for a
// usage error.
func runReencrypt(ctx context.Context, args []string, deps reencryptDeps) int {
	out := deps.Out
	flags := flag.NewFlagSet("reencrypt", flag.ContinueOnError)
	flags.SetOutput(out)
	dryRun := flags.Bool("dry-run", false, "")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 {
		fmt.Fprint(out, reencryptUsage)
		return 2
	}
	if *dryRun {
		fmt.Fprintln(out, "Re-encryption pass, dry run: nothing is changed.")
	} else {
		fmt.Fprintln(out, "Re-encryption pass.")
	}
	reports, err := reencrypt.Run(ctx, deps.Stores, reencrypt.Options{DryRun: *dryRun})
	for _, r := range reports {
		printReencryptReport(out, r, *dryRun)
	}
	if err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	remaining := reencrypt.OldFormatRemaining(reports)
	switch {
	case remaining == 0:
		fmt.Fprintln(out, "✓ No old-format value remains in any store.")
	case *dryRun:
		fmt.Fprintf(out, "! %d old-format value(s) would be rewritten: run `mypa reencrypt` without --dry-run.\n", remaining)
	default:
		fmt.Fprintf(out, "✗ %d old-format value(s) remain: they changed while the pass ran. Run it again.\n", remaining)
		return 1
	}
	return 0
}

func printReencryptReport(out io.Writer, r reencrypt.Report, dryRun bool) {
	fmt.Fprintf(out, "\n%s\n", r.Store)
	fmt.Fprintf(out, "  values:               %d\n", r.Values)
	fmt.Fprintf(out, "  current format:       %d\n", r.Current)
	fmt.Fprintf(out, "  old format found:     %d\n", r.OldFormat)
	if !dryRun {
		fmt.Fprintf(out, "  rewritten:            %d\n", r.Rewritten)
	}
	fmt.Fprintf(out, "  old format remaining: %d\n", r.Remaining)
	if r.Unreadable > 0 {
		fmt.Fprintf(out, "  ! not readable with this key: %d (left as they are; another key, a damaged value, or not encrypted)\n", r.Unreadable)
	}
}
