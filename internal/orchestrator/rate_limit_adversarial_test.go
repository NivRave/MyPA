package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"go.uber.org/mock/gomock"
	"github.com/nivik/mypa/internal/calendar"
	"github.com/nivik/mypa/internal/config"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator/mocks"
	"github.com/nivik/mypa/internal/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdversarial_Engine_LockoutBlocksValidCode verifies that once a user is locked out
// after 5 failed attempts, even submitting a valid 6-digit pairing code is strictly blocked.
func TestAdversarial_Engine_LockoutBlocksValidCode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tgMock := mocks.NewMockTelegramClient(ctrl)
	dbMock := mocks.NewMockDBClient(ctrl)
	eventPublisherMock := mocks.NewMockEventPublisher(ctrl)
	eventPublisherMock.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	oauthCfg := calendar.NewOAuthConfig(&config.GoogleConfig{
		ClientID:     "test",
		ClientSecret: "secret",
		RedirectURL:  "url",
	})

	engine := NewEngine(
		nil,
		store,
		dbMock,
		nil,
		tgMock,
		nil,
		oauthCfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		eventPublisherMock,
		"UTC",
	)
	defer engine.Wait()

	userID := "adv-lockout-attacker"

	// Mock DB: user not found initially
	dbMock.EXPECT().GetUser(userID).Return(nil, assert.AnError).AnyTimes()
	// Mock DB: invalid codes return error
	dbMock.EXPECT().GetPairingCode("000000").Return(nil, assert.AnError).AnyTimes()

	// 1-4 failures
	for i := 1; i <= 4; i++ {
		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
			str, ok := x.(string)
			return ok && strings.Contains(str, "attempts remaining")
		})).Return(nil).Times(1)

		err := engine.ProcessMessage(context.Background(), models.Message{
			UserID: userID,
			Text:   "000000",
			Source: "telegram",
		})
		require.NoError(t, err)
	}

	// 5th failure: locks out
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		str, ok := x.(string)
		return ok && strings.Contains(str, "locked for 1 hour")
	})).Return(nil).Times(1)

	err = engine.ProcessMessage(context.Background(), models.Message{
		UserID: userID,
		Text:   "000000",
		Source: "telegram",
	})
	require.NoError(t, err)

	// Now on 6th attempt, attacker submits the VALID 6-digit code "123456"
	// The system MUST NOT redeem it or even check the DB for it - must immediately return lockout!
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		str, ok := x.(string)
		return ok && strings.Contains(str, "temporarily locked out")
	})).Return(nil).Times(1)

	// Note: We do NOT set an expectation on GetPairingCode("123456") or RedeemPairingCode!
	// If the engine attempted to call them, gomock would fail!
	err = engine.ProcessMessage(context.Background(), models.Message{
		UserID: userID,
		Text:   "123456", // valid code submitted while locked out
		Source: "telegram",
	})
	require.NoError(t, err)
}

// TestAdversarial_Engine_SuccessfulPairingResetsCounter verifies that if an unauthorized user
// fails 3 times, then enters the valid code on attempt 4, the pairing succeeds and the Redis rate
// limit is completely cleared.
func TestAdversarial_Engine_SuccessfulPairingResetsCounter(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tgMock := mocks.NewMockTelegramClient(ctrl)
	dbMock := mocks.NewMockDBClient(ctrl)
	eventPublisherMock := mocks.NewMockEventPublisher(ctrl)
	eventPublisherMock.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	store, err := state.NewStore("redis://" + mr.Addr())
	require.NoError(t, err)
	defer store.Close()

	oauthCfg := calendar.NewOAuthConfig(&config.GoogleConfig{
		ClientID:     "test",
		ClientSecret: "secret",
		RedirectURL:  "url",
	})

	engine := NewEngine(
		nil,
		store,
		dbMock,
		nil,
		tgMock,
		nil,
		oauthCfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		eventPublisherMock,
		"UTC",
	)
	defer engine.Wait()

	userID := "adv-success-reset-user"

	// Mock DB: user not found initially
	dbMock.EXPECT().GetUser(userID).Return(nil, assert.AnError).Times(4)
	// Failed codes
	dbMock.EXPECT().GetPairingCode("000000").Return(nil, assert.AnError).Times(3)

	// 3 failed attempts
	for i := 1; i <= 3; i++ {
		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
			str, ok := x.(string)
			return ok && strings.Contains(str, "attempts remaining")
		})).Return(nil).Times(1)

		err := engine.ProcessMessage(context.Background(), models.Message{
			UserID: userID,
			Text:   "000000",
			Source: "telegram",
		})
		require.NoError(t, err)
	}

	// Verify attempt counter is 3 in Redis
	val, err := mr.Get("rate_limit:pairing:" + userID)
	require.NoError(t, err)
	assert.Equal(t, "3", val)

	// Valid code setup
	validPC := &models.PairingCode{
		ID:          "pc-uuid-123",
		Code:        "999888",
		Role:        "member",
		FamilyGroup: "family-adv",
	}
	dbMock.EXPECT().GetPairingCode("999888").Return(validPC, nil).Times(1)
	dbMock.EXPECT().RedeemPairingCode("pc-uuid-123", gomock.Any()).Return(nil).Times(1)

	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		str, ok := x.(string)
		return ok && strings.Contains(str, "Pairing successful")
	})).Return(nil).Times(1)

	// Attempt 4: valid code
	err = engine.ProcessMessage(context.Background(), models.Message{
		UserID: userID,
		Text:   "/start 999888",
		Source: "telegram",
	})
	require.NoError(t, err)

	// Verify Redis keys are deleted upon success
	assert.False(t, mr.Exists("rate_limit:pairing:"+userID), "Rate limit key must be cleared on successful pairing")
	assert.False(t, mr.Exists("rate_limit:pairing:lockout:"+userID), "Lockout key must be cleared on successful pairing")
}
