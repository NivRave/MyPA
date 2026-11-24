package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// TokenCiphertexts is the Google sign-ins in Redis (token:<user ID>) as a store of the
// re-encryption pass (V4-07, reencrypt.Store). It reads and writes the stored bytes as they are;
// it never decrypts.
type TokenCiphertexts struct {
	client *redis.Client
}

// TokenStore returns the cached Google sign-ins as a re-encryption store.
func (s *Store) TokenStore() TokenCiphertexts {
	return TokenCiphertexts{client: s.client}
}

// Name implements reencrypt.Store.
func (TokenCiphertexts) Name() string { return "Google sign-ins in Redis (token:*)" }

// Each implements reencrypt.Store: every token:* key, once, with its stored bytes.
func (t TokenCiphertexts) Each(ctx context.Context, fn func(key string, value []byte) error) error {
	var keys []string
	seen := map[string]bool{}
	iter := t.client.Scan(ctx, 0, "token:*", 200).Iterator()
	for iter.Next(ctx) {
		if k := iter.Val(); !seen[k] { // SCAN may return a key more than once
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("list cached tokens: %w", err)
	}
	for _, k := range keys {
		value, err := t.client.Get(ctx, k).Bytes()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read a cached token: %w", err)
		}
		if err := fn(k, value); err != nil {
			return err
		}
	}
	return nil
}

// replaceIfUnchanged sets KEYS[1] to ARGV[2], keeping its expiry, only while it holds ARGV[1].
var replaceIfUnchanged = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  redis.call('SET', KEYS[1], ARGV[2], 'KEEPTTL')
  return 1
end
return 0`)

// Replace implements reencrypt.Store, atomically: a token written again since it was read is kept.
func (t TokenCiphertexts) Replace(ctx context.Context, key string, old, value []byte) (bool, error) {
	n, err := replaceIfUnchanged.Run(ctx, t.client, []string{key}, old, value).Int()
	if err != nil {
		return false, fmt.Errorf("rewrite a cached token: %w", err)
	}
	return n == 1, nil
}
