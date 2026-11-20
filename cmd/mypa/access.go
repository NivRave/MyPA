package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/db"
)

// `mypa access grant owner|operator <person>`: the offline recovery command beside `transfer`
// (ADR 0002 §10, V4-35). Migration C gives no owner to a household that had no admin, and /doctor
// reports it; the operator assigns one here. It runs as a one-shot process with the app's
// settings, against the database, and never moves anyone to another household.
const accessUsage = `mypa access grant owner <person>     make the person an owner of their own household
mypa access grant operator <person>  make the person the install's operator

<person> is the ID a member writes from (their sender ID on a channel), a channel account ID, or a
person ID. Run it where the app runs, for example:

  docker compose run --rm app access grant owner 123456789
`

// accessGranter is what the command needs; db.Client implements it.
type accessGranter interface {
	GrantAccess(ctx context.Context, key string, g db.Grant) (db.GrantResult, error)
}

func accessMain(args []string) int {
	out := os.Stdout
	if len(args) != 3 || args[0] != "grant" {
		fmt.Fprint(out, accessUsage)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(out, "✗ cannot load the configuration: %v\n", err)
		return 1
	}
	client, err := db.Open(cfg.Database.URL)
	if err != nil {
		fmt.Fprintf(out, "✗ cannot connect to the database: %v\n", err)
		return 1
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return runAccess(ctx, out, client, args)
}

// runAccess runs `grant <what> <person>` against g and reports to out.
func runAccess(ctx context.Context, out io.Writer, g accessGranter, args []string) int {
	if len(args) != 3 || args[0] != "grant" {
		fmt.Fprint(out, accessUsage)
		return 2
	}
	what := db.Grant(strings.ToLower(strings.TrimSpace(args[1])))
	if what != db.GrantOwner && what != db.GrantOperator {
		fmt.Fprintf(out, "✗ unknown grant %q: owner or operator\n\n%s", args[1], accessUsage)
		return 2
	}
	res, err := g.GrantAccess(ctx, args[2], what)
	if err != nil {
		fmt.Fprintf(out, "✗ %v\n", err)
		return 1
	}
	switch {
	case !res.Changed:
		fmt.Fprintf(out, "✓ person %s already is %s; nothing changed\n", res.PersonID, article(what))
	default:
		fmt.Fprintf(out, "✓ person %s is now %s (household %s)\n", res.PersonID, article(what), res.HouseholdID)
	}
	return 0
}

func article(g db.Grant) string {
	if g == db.GrantOwner {
		return "an owner of their household"
	}
	return "the operator"
}
