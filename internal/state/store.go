package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nivik/mypa/internal/models"
	"github.com/redis/go-redis/v9"
)

// Store handles all state persistence in Redis.
type Store struct {
	client *redis.Client
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

// SetOAuthToken stores a Google OAuth2 token for a user.
func (s *Store) SetOAuthToken(ctx context.Context, userID string, tokenBytes []byte) error {
	key := fmt.Sprintf("token:%s", userID)
	// Tokens are long-lived, we won't set a TTL, or we could set a long one
	return s.client.Set(ctx, key, tokenBytes, 0).Err()
}

// GetOAuthToken retrieves a Google OAuth2 token for a user.
func (s *Store) GetOAuthToken(ctx context.Context, userID string) ([]byte, error) {
	key := fmt.Sprintf("token:%s", userID)
	res, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil // No token found
	} else if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}
	return res, nil
}

