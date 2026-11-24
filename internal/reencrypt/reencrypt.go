// Package reencrypt is the re-encryption pass (V4-07, BL-16): it rewrites every value still in the
// old, unsalted encryption format in the current, salted one, and reports how many old-format
// values remain in each store. V4-45 removes the unsalted fallback in internal/crypto once the
// pass, run on the live install, has reported zero in every store (DE-12).
//
// A store is any place that holds encrypted values. The command (`mypa reencrypt`) runs the pass
// over the three that exist: the Google sign-ins in Redis, the credential fallback table and the
// API connector headers.
package reencrypt

import (
	"bytes"
	"context"
	"fmt"

	"github.com/nivik/mypa/internal/crypto"
)

// Store is one place that holds encrypted values.
type Store interface {
	// Name says what the store is, for the report.
	Name() string
	// Each calls fn with every stored value and a key that names it within the store. A value that
	// disappears while the store is read is skipped.
	Each(ctx context.Context, fn func(key string, value []byte) error) error
	// Replace writes value under key in place of old, only while the key still holds old, and
	// reports whether it did. A value changed or deleted since it was read (the app wrote it
	// again) is left alone and counted as remaining; the next run sees what the app wrote.
	Replace(ctx context.Context, key string, old, value []byte) (bool, error)
}

// Report is what the pass found in one store. It holds counts only, never keys or values.
type Report struct {
	Store string
	// Values is how many values the store holds.
	Values int
	// Current is how many were already in the current format.
	Current int
	// OldFormat is how many were in the old, unsalted format.
	OldFormat int
	// Rewritten is how many of those the pass rewrote in the current format (none in a dry run).
	Rewritten int
	// Remaining is how many old-format values the store still holds after the pass.
	Remaining int
	// Unreadable is how many opened in neither format with this key (another key, a damaged value,
	// or a value that is not encrypted). The pass leaves them as they are.
	Unreadable int
}

// Options configure a pass.
type Options struct {
	// DryRun only counts: nothing is written.
	DryRun bool
	// Passphrase is the encryption secret; empty means ENCRYPTION_KEY, as everywhere else.
	Passphrase string
}

// Run makes the pass over each store in turn and returns one report per store. It stops at the
// first store that cannot be read or written; the reports of the stores before it are returned.
func Run(ctx context.Context, stores []Store, opts Options) ([]Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var reports []Report
	for _, s := range stores {
		r, err := pass(ctx, s, opts)
		if err != nil {
			return reports, fmt.Errorf("%s: %w", s.Name(), err)
		}
		reports = append(reports, r)
	}
	return reports, nil
}

func pass(ctx context.Context, s Store, opts Options) (Report, error) {
	r := Report{Store: s.Name()}
	err := s.Each(ctx, func(key string, value []byte) error {
		r.Values++
		plaintext, format, err := crypto.Inspect(value, opts.Passphrase)
		if err != nil {
			return err
		}
		switch format {
		case crypto.FormatCurrent:
			r.Current++
			return nil
		case crypto.FormatUnreadable:
			r.Unreadable++
			return nil
		}
		r.OldFormat++
		if opts.DryRun {
			return nil
		}
		rewritten, err := crypto.Encrypt(plaintext, opts.Passphrase)
		if err != nil {
			return err
		}
		// The new value must open, without the fallback, to the same plaintext before it replaces
		// the old one.
		check, err := crypto.DecryptCurrent(rewritten, opts.Passphrase)
		if err != nil || !bytes.Equal(check, plaintext) {
			return fmt.Errorf("a rewritten value does not decrypt to its plaintext")
		}
		replaced, err := s.Replace(ctx, key, value, rewritten)
		if err != nil {
			return err
		}
		if replaced {
			r.Rewritten++
		}
		return nil
	})
	if err != nil {
		return r, err
	}
	r.Remaining = r.OldFormat - r.Rewritten
	return r, nil
}

// OldFormatRemaining is the number of old-format values the reports leave, over every store.
func OldFormatRemaining(reports []Report) int {
	n := 0
	for _, r := range reports {
		n += r.Remaining
	}
	return n
}
