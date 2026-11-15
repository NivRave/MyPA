package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/models"
	"github.com/redis/go-redis/v9"
)

// TokenFallbackGetter retrieves an encrypted token fallback from persistent storage.
type TokenFallbackGetter func(ctx context.Context, userID string) ([]byte, error)

// Store handles all state persistence in Redis.
type Store struct {
	client         *redis.Client
	fallbackGetter TokenFallbackGetter
}

// NewStore initializes a new Redis store.
func NewStore(redisURL string) (*Store, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis url: %w", err)
	}

	client := redis.NewClient(opts)

	// Ping to verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &Store{client: client}, nil
}

// Close closes the Redis connection.
func (s *Store) Close() error {
	return s.client.Close()
}

// formatAttachmentMarker formats a concise human-readable metadata marker for stripped attachments.
func formatAttachmentMarker(mimeType string, dataLen int) string {
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	sizeKB := float64(dataLen) / 1024.0
	if sizeKB >= 1024.0 {
		return fmt.Sprintf("[Attachment: %s (%.1f MB)]", mimeType, sizeKB/1024.0)
	}
	return fmt.Sprintf("[Attachment: %s (%.1f KB)]", mimeType, sizeKB)
}

// AppendChatHistory adds a message to the user's conversation history.
// SCALE-02 Defense-in-depth: guarantees raw PhotoData bytes are never persisted to Redis.
func (s *Store) AppendChatHistory(ctx context.Context, userID string, msg models.ChatMessage) error {
	if len(msg.PhotoData) > 0 {
		marker := formatAttachmentMarker(msg.PhotoMimeType, len(msg.PhotoData))
		if strings.TrimSpace(msg.Content) == "" {
			msg.Content = marker
		} else if !strings.Contains(msg.Content, "[Attachment:") {
			msg.Content = fmt.Sprintf("%s\n%s", strings.TrimSpace(msg.Content), marker)
		}
		msg.PhotoData = nil
		msg.PhotoMimeType = ""
	}

	key := fmt.Sprintf("chat:%s", userID)
	
	bytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal chat message: %w", err)
	}

	pipe := s.client.Pipeline()
	// Add to list (right push)
	pipe.RPush(ctx, key, bytes)
	// Keep only the last 20 messages
	pipe.LTrim(ctx, key, -20, -1)
	// Refresh TTL to 1 hour
	pipe.Expire(ctx, key, 1*time.Hour)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to append chat history: %w", err)
	}

	return nil
}

// GetChatHistory retrieves the recent conversation history for a user.
// SCALE-02 Defense-in-depth: clears any legacy PhotoData bytes on unmarshal.
func (s *Store) GetChatHistory(ctx context.Context, userID string) ([]models.ChatMessage, error) {
	key := fmt.Sprintf("chat:%s", userID)

	results, err := s.client.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get chat history: %w", err)
	}

	var history []models.ChatMessage
	for _, res := range results {
		var msg models.ChatMessage
		if err := json.Unmarshal([]byte(res), &msg); err != nil {
			// Skip corrupted messages but log it in real app
			continue
		}
		if len(msg.PhotoData) > 0 {
			msg.PhotoData = nil
			msg.PhotoMimeType = ""
		}
		history = append(history, msg)
	}

	return history, nil
}

// SetCredentialFallback configures a persistent fallback getter (e.g. PostgreSQL encrypted fallback).
func (s *Store) SetCredentialFallback(getter TokenFallbackGetter) {
	s.fallbackGetter = getter
}

// SetOAuthToken encrypts tokenBytes using AES-256-GCM and persists them in Redis.
func (s *Store) SetOAuthToken(ctx context.Context, userID string, tokenBytes []byte) error {
	key := fmt.Sprintf("token:%s", userID)
	encrypted, err := crypto.Encrypt(tokenBytes, "")
	if err != nil {
		return fmt.Errorf("failed to encrypt oauth token: %w", err)
	}
	return s.client.Set(ctx, key, encrypted, 0).Err()
}

// GetOAuthToken retrieves and decrypts the Google OAuth2 token for a user.
// If the token is missing from Redis, it invokes the optional fallback retriever.
func (s *Store) GetOAuthToken(ctx context.Context, userID string) ([]byte, error) {
	key := fmt.Sprintf("token:%s", userID)
	res, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil || len(res) == 0 {
		if s.fallbackGetter != nil {
			fallbackBytes, fbErr := s.fallbackGetter(ctx, userID)
			if fbErr == nil && len(fallbackBytes) > 0 {
				_ = s.SetOAuthToken(ctx, userID, fallbackBytes)
				return fallbackBytes, nil
			}
		}
		return nil, nil // No token found
	} else if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	plaintext, decErr := crypto.Decrypt(res, "")
	if decErr != nil {
		// Graceful migration: check if res is legacy unencrypted JSON
		var legacyTok map[string]interface{}
		if json.Unmarshal(res, &legacyTok) == nil && legacyTok["access_token"] != nil {
			_ = s.SetOAuthToken(ctx, userID, res)
			return res, nil
		}
		return nil, fmt.Errorf("failed to decrypt oauth token from redis: %w", decErr)
	}

	return plaintext, nil
}

// SaveOAuthState stores a 32-byte hex nonce mapping to the userID with a TTL.
func (s *Store) SaveOAuthState(ctx context.Context, nonce, userID string, ttl time.Duration) error {
	key := fmt.Sprintf("oauth_state:%s", nonce)
	return s.client.Set(ctx, key, userID, ttl).Err()
}

// ValidateAndDeleteOAuthState atomically retrieves and removes the OAuth state nonce.
func (s *Store) ValidateAndDeleteOAuthState(ctx context.Context, nonce string) (string, error) {
	if strings.TrimSpace(nonce) == "" {
		return "", errors.New("empty oauth state nonce")
	}
	key := fmt.Sprintf("oauth_state:%s", nonce)
	val, err := s.client.GetDel(ctx, key).Result()
	if err == redis.Nil {
		return "", errors.New("invalid or expired oauth state nonce")
	} else if err != nil {
		return "", fmt.Errorf("failed to validate oauth state: %w", err)
	}
	return val, nil
}

// PendingMetaToolAction represents an interactive confirmation payload for privileged actions.
type PendingMetaToolAction struct {
	ToolName  string                 `json:"tool_name"`
	Args      map[string]interface{} `json:"args"`
	UserID    string                 `json:"user_id"`
	CreatedAt time.Time              `json:"created_at"`
}

// StagePendingMetaTool stages a pending tool action in Redis with a TTL.
func (s *Store) StagePendingMetaTool(ctx context.Context, userID string, action PendingMetaToolAction, ttl time.Duration) error {
	key := fmt.Sprintf("pending_meta_tool:%s", userID)
	data, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("failed to marshal pending meta tool action: %w", err)
	}
	return s.client.Set(ctx, key, data, ttl).Err()
}

// GetAndClearPendingMetaTool retrieves and clears a staged pending tool action.
func (s *Store) GetAndClearPendingMetaTool(ctx context.Context, userID string) (*PendingMetaToolAction, error) {
	key := fmt.Sprintf("pending_meta_tool:%s", userID)
	data, err := s.client.GetDel(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to get and clear pending meta tool: %w", err)
	}
	var action PendingMetaToolAction
	if err := json.Unmarshal(data, &action); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pending meta tool: %w", err)
	}
	return &action, nil
}

// RecordFailedPairingAttempt increments the failed pairing counter for a user in Redis.
// Enforces a 15-minute rolling window on initial failure. On the 5th failure, triggers a 1-hour lockout.
func (s *Store) RecordFailedPairingAttempt(ctx context.Context, userID string) (int64, bool, error) {
	key := fmt.Sprintf("rate_limit:pairing:%s", userID)
	lockoutKey := fmt.Sprintf("rate_limit:pairing:lockout:%s", userID)

	// If already locked out, return immediately
	ttl, err := s.client.TTL(ctx, lockoutKey).Result()
	if err == nil && ttl > 0 {
		return 5, true, nil
	}

	attempts, err := s.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, false, fmt.Errorf("failed to increment pairing rate limit: %w", err)
	}

	if attempts == 1 {
		_ = s.client.Expire(ctx, key, 15*time.Minute).Err()
	} else if attempts >= 5 {
		_ = s.client.Set(ctx, lockoutKey, "locked", 1*time.Hour).Err()
		_ = s.client.Expire(ctx, key, 1*time.Hour).Err()
		return attempts, true, nil
	}

	return attempts, false, nil
}

// CheckPairingLockout returns true and the remaining duration if the user is currently locked out.
func (s *Store) CheckPairingLockout(ctx context.Context, userID string) (bool, time.Duration, error) {
	lockoutKey := fmt.Sprintf("rate_limit:pairing:lockout:%s", userID)
	ttl, err := s.client.TTL(ctx, lockoutKey).Result()
	if err != nil {
		return false, 0, fmt.Errorf("failed to check pairing lockout: %w", err)
	}
	if ttl > 0 {
		return true, ttl, nil
	}
	return false, 0, nil
}

// ResetPairingRateLimit clears all pairing rate limits and lockouts for a user upon successful authentication.
func (s *Store) ResetPairingRateLimit(ctx context.Context, userID string) error {
	key := fmt.Sprintf("rate_limit:pairing:%s", userID)
	lockoutKey := fmt.Sprintf("rate_limit:pairing:lockout:%s", userID)
	return s.client.Del(ctx, key, lockoutKey).Err()
}

const (
	PromptCachePrefix     = "ctx_prompt"
	DefaultPromptCacheTTL = 10 * time.Minute
)

// promptKeySegment percent-encodes '%' and ':' so that user-controlled identifiers (family group
// names, platform IDs) can never shift segment boundaries: ("a:b","c") and ("a","b:c") stay distinct.
var promptKeySegmentEncoder = strings.NewReplacer("%", "%25", ":", "%3A")

func promptKeySegment(s string) string {
	return promptKeySegmentEncoder.Replace(strings.TrimSpace(s))
}

// globEscaper escapes Redis glob metacharacters so a segment is matched literally in SCAN MATCH.
var globEscaper = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`)

// PromptCacheKey builds ctx_prompt:<familyGroup>:<userID>:<module> with encoded segments.
// Sections are per user because several modules (memory, automation, schedule) render
// personal data; a family-only key leaked one member's sections to the others.
func PromptCacheKey(familyGroup, userID, module string) string {
	return fmt.Sprintf("%s:%s:%s:%s", PromptCachePrefix,
		promptKeySegment(familyGroup), promptKeySegment(userID), promptKeySegment(module))
}

func promptScopeValid(familyGroup, userID, module string) bool {
	return strings.TrimSpace(familyGroup) != "" && strings.TrimSpace(userID) != "" && strings.TrimSpace(module) != ""
}

// SetPromptSection stores a generated system prompt section for one user in Redis with a TTL.
func (s *Store) SetPromptSection(ctx context.Context, familyGroup, userID, module, content string, ttl time.Duration) error {
	if !promptScopeValid(familyGroup, userID, module) {
		return errors.New("familyGroup, userID and module cannot be empty")
	}
	if ttl <= 0 {
		ttl = DefaultPromptCacheTTL
	}
	return s.client.Set(ctx, PromptCacheKey(familyGroup, userID, module), content, ttl).Err()
}

// GetPromptSection retrieves a cached system prompt section for a user and module.
func (s *Store) GetPromptSection(ctx context.Context, familyGroup, userID, module string) (string, bool, error) {
	if !promptScopeValid(familyGroup, userID, module) {
		return "", false, nil
	}
	val, err := s.client.Get(ctx, PromptCacheKey(familyGroup, userID, module)).Result()
	if err == redis.Nil {
		return "", false, nil
	} else if err != nil {
		return "", false, fmt.Errorf("failed to get prompt section: %w", err)
	}
	return val, true, nil
}

// GetPromptSections fetches multiple prompt sections in a single roundtrip via MGet.
// On Redis error, it degrades gracefully and treats all requested modules as cache misses.
func (s *Store) GetPromptSections(ctx context.Context, familyGroup, userID string, modules []string) (map[string]string, []string, error) {
	hits := make(map[string]string)
	if len(modules) == 0 {
		return hits, nil, nil
	}
	if strings.TrimSpace(familyGroup) == "" || strings.TrimSpace(userID) == "" {
		return hits, modules, nil
	}

	keys := make([]string, len(modules))
	for i, m := range modules {
		keys[i] = PromptCacheKey(familyGroup, userID, m)
	}

	vals, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		// On Redis network error, degrade gracefully: treat all as cache misses
		return hits, modules, err
	}

	var misses []string
	for i, val := range vals {
		mod := modules[i]
		if val != nil {
			if str, ok := val.(string); ok {
				hits[mod] = str
				continue
			}
		}
		misses = append(misses, mod)
	}
	return hits, misses, nil
}

// InvalidatePromptSections removes cached prompt sections for multiple keys in a single DEL command.
func (s *Store) InvalidatePromptSections(ctx context.Context, keys ...string) error {
	var validKeys []string
	for _, k := range keys {
		if strings.TrimSpace(k) != "" {
			validKeys = append(validKeys, strings.TrimSpace(k))
		}
	}
	if len(validKeys) == 0 {
		return nil
	}
	return s.client.Del(ctx, validKeys...).Err()
}

// InvalidateFamilyPromptSections deletes the cached sections of the given modules for EVERY user
// in a family scope (ctx_prompt:<scope>:*:<module>). It uses SCAN (non-blocking, unlike KEYS)
// with glob-escaped segments, and returns the number of keys deleted.
func (s *Store) InvalidateFamilyPromptSections(ctx context.Context, scope string, modules []string) (int, error) {
	if strings.TrimSpace(scope) == "" || len(modules) == 0 {
		return 0, nil
	}
	suffixes := make(map[string]bool, len(modules))
	for _, m := range modules {
		if strings.TrimSpace(m) != "" {
			suffixes[":"+promptKeySegment(m)] = true
		}
	}

	pattern := fmt.Sprintf("%s:%s:*", PromptCachePrefix, globEscaper.Replace(promptKeySegment(scope)))
	var toDelete []string
	iter := s.client.Scan(ctx, 0, pattern, 500).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		if idx := strings.LastIndex(key, ":"); idx >= 0 && suffixes[key[idx:]] {
			toDelete = append(toDelete, key)
		}
	}
	if err := iter.Err(); err != nil {
		return 0, fmt.Errorf("scan prompt cache keys: %w", err)
	}
	if len(toDelete) == 0 {
		return 0, nil
	}
	n, err := s.client.Del(ctx, toDelete...).Result()
	return int(n), err
}



