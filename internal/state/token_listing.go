package state

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// OAuthTokenUserIDs lists the users that have a Google sign-in in the cache (V4-43). The
// restore-and-migrate tool uses it to make sure each of them also has the encrypted copy in the
// database before a backup is taken. It reads keys only, never values.
func (s *Store) OAuthTokenUserIDs(ctx context.Context) ([]string, error) {
	var ids []string
	iter := s.client.Scan(ctx, 0, "token:*", 200).Iterator()
	for iter.Next(ctx) {
		ids = append(ids, strings.TrimPrefix(iter.Val(), "token:"))
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("failed to list cached tokens: %w", err)
	}
	sort.Strings(ids)
	return ids, nil
}
