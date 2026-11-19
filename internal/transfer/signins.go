package transfer

import (
	"context"
	"fmt"
	"os"

	"github.com/nivik/mypa/internal/db"
)

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// SignInSync makes sure every Google sign-in is in the database before a backup is taken.
//
// A sign-in is stored in the cache (Redis) and, encrypted, in the database. Only the database is
// backed up. The database copy is written when someone finishes /connect, so a sign-in from
// before that copy existed lives in the cache alone and would be lost by a restore. Sync copies
// such sign-ins into the database. It never overwrites an existing copy and never touches the
// cache.
type SignInSync struct {
	// CachedUsers lists the users that have a sign-in in the cache.
	CachedUsers func(ctx context.Context) ([]string, error)
	// CachedToken returns a user's sign-in from the cache, decrypted.
	CachedToken func(ctx context.Context, userID string) ([]byte, error)
	// HasCopy reports whether the database already holds a copy for the user.
	HasCopy func(ctx context.Context, userID string) (bool, error)
	// SaveCopy stores the encrypted copy in the database.
	SaveCopy func(ctx context.Context, userID string, token []byte) error
	// CountCopies returns how many copies the database holds afterwards.
	CountCopies func(ctx context.Context) (int, error)
}

// Run performs the sync and reports what the backup will carry.
func (s SignInSync) Run(ctx context.Context) (db.SignInInfo, error) {
	var info db.SignInInfo
	users, err := s.CachedUsers(ctx)
	if err != nil {
		return info, fmt.Errorf("list cached sign-ins: %w", err)
	}
	for _, userID := range users {
		has, err := s.HasCopy(ctx, userID)
		if err != nil {
			return info, fmt.Errorf("look up a database copy: %w", err)
		}
		if has {
			continue
		}
		token, err := s.CachedToken(ctx, userID)
		if err != nil || len(token) == 0 {
			info.Unreadable++ // counted, never named: the report must not list people
			continue
		}
		if err := s.SaveCopy(ctx, userID, token); err != nil {
			return info, fmt.Errorf("save a database copy: %w", err)
		}
		info.CopiedNow++
	}
	if info.InDatabase, err = s.CountCopies(ctx); err != nil {
		return info, fmt.Errorf("count database copies: %w", err)
	}
	return info, nil
}
