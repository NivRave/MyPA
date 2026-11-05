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

// AppendChatHistory adds a message to the user's conversation history.
func (s *Store) AppendChatHistory(ctx context.Context, userID string, msg models.ChatMessage) error {
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


