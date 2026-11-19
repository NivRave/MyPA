package transfer

import (
	"context"
	"errors"
	"testing"

	"github.com/nivik/mypa/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type signInFixture struct {
	cache   map[string][]byte // user -> token in the cache (nil value: present but unreadable)
	copies  map[string][]byte // user -> copy in the database
	saveErr error
}

func (f *signInFixture) sync() SignInSync {
	return SignInSync{
		CachedUsers: func(context.Context) ([]string, error) {
			var users []string
			for u := range f.cache {
				users = append(users, u)
			}
			return users, nil
		},
		CachedToken: func(_ context.Context, userID string) ([]byte, error) {
			if f.cache[userID] == nil {
				return nil, errors.New("failed to decrypt oauth token from redis")
			}
			return f.cache[userID], nil
		},
		HasCopy: func(_ context.Context, userID string) (bool, error) {
			_, ok := f.copies[userID]
			return ok, nil
		},
		SaveCopy: func(_ context.Context, userID string, token []byte) error {
			if f.saveErr != nil {
				return f.saveErr
			}
			f.copies[userID] = token
			return nil
		},
		CountCopies: func(context.Context) (int, error) { return len(f.copies), nil },
	}
}

// V4-43: Google sign-ins are restored from their copy in the database, so every sign-in must
// have one before the backup is taken.
func TestSignInSync_CopiesOnlyWhatIsMissing(t *testing.T) {
	f := &signInFixture{
		cache: map[string][]byte{
			"1001": []byte("dana-new"),   // has a copy already
			"1002": []byte("omer-token"), // connected before copies existed
			"1003": nil,                  // cached but unreadable
		},
		copies: map[string][]byte{
			"1001": []byte("dana-original"),
			"1009": []byte("someone-not-in-the-cache"),
		},
	}

	info, err := f.sync().Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, db.SignInInfo{InDatabase: 3, CopiedNow: 1, Unreadable: 1}, info)
	assert.Equal(t, []byte("omer-token"), f.copies["1002"], "the missing copy was written")
	assert.Equal(t, []byte("dana-original"), f.copies["1001"], "an existing copy is never overwritten")
	_, copied := f.copies["1003"]
	assert.False(t, copied)
}

func TestSignInSync_NothingCachedIsFine(t *testing.T) {
	f := &signInFixture{cache: map[string][]byte{}, copies: map[string][]byte{"1001": []byte("t")}}
	info, err := f.sync().Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, db.SignInInfo{InDatabase: 1}, info)
}

func TestSignInSync_AFailedWriteStopsTheExport(t *testing.T) {
	f := &signInFixture{cache: map[string][]byte{"1002": []byte("t")}, copies: map[string][]byte{}, saveErr: errors.New("database is read-only")}
	_, err := f.sync().Run(context.Background())
	require.ErrorContains(t, err, "save a database copy")
}
