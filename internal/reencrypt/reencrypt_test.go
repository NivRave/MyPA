package reencrypt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/crypto/cryptotest"
	"github.com/nivik/mypa/internal/reencrypt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const key = "reencrypt-unit-key"

// fakeStore holds values in memory; changeBeforeReplace makes the app "write" a key between the
// pass reading it and replacing it.
type fakeStore struct {
	values              map[string][]byte
	order               []string
	changeBeforeReplace string
	failEach            error
}

func (f *fakeStore) Name() string { return "fake" }

func (f *fakeStore) Each(_ context.Context, fn func(string, []byte) error) error {
	if f.failEach != nil {
		return f.failEach
	}
	for _, k := range f.order {
		if err := fn(k, f.values[k]); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeStore) Replace(_ context.Context, k string, old, value []byte) (bool, error) {
	if k == f.changeBeforeReplace {
		f.values[k] = []byte("written again by the app")
	}
	if string(f.values[k]) != string(old) {
		return false, nil
	}
	f.values[k] = value
	return true, nil
}

// A value the app writes again while the pass runs is kept and counted as remaining, so the
// report never claims zero for a store it did not finish; the next run sees the new value.
func TestRun_AValueChangedDuringThePassIsKeptAndCountedAsRemaining(t *testing.T) {
	s := &fakeStore{
		values: map[string][]byte{
			"a": cryptotest.Unsalted(t, []byte("one"), key),
			"b": cryptotest.Unsalted(t, []byte("two"), key),
		},
		order:               []string{"a", "b"},
		changeBeforeReplace: "b",
	}
	reports, err := reencrypt.Run(context.Background(), []reencrypt.Store{s}, reencrypt.Options{Passphrase: key})
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, reencrypt.Report{Store: "fake", Values: 2, OldFormat: 2, Rewritten: 1, Remaining: 1}, reports[0])
	assert.Equal(t, 1, reencrypt.OldFormatRemaining(reports))
	assert.Equal(t, []byte("written again by the app"), s.values["b"])
	got, err := crypto.DecryptCurrent(s.values["a"], key)
	require.NoError(t, err)
	assert.Equal(t, []byte("one"), got)
}

// A store that cannot be read stops the pass with its name; the stores before it are reported.
func TestRun_StopsAtAStoreThatFails(t *testing.T) {
	ok := &fakeStore{values: map[string][]byte{}}
	broken := &fakeStore{failEach: errors.New("connection refused")}
	reports, err := reencrypt.Run(context.Background(), []reencrypt.Store{ok, broken}, reencrypt.Options{Passphrase: key})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fake: connection refused")
	assert.Len(t, reports, 1)
}
