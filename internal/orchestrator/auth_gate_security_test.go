package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// TestAdversarial_SEC01_UnauthenticatedCommandsBlocked verifies that unauthenticated users
// attempting /connect, /help, /backup, /restore, or media are rejected immediately with the pairing prompt.
func TestAdversarial_SEC01_UnauthenticatedCommandsBlocked(t *testing.T) {
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()

	blockedCommands := []string{"/connect", "/help", "/backup", "/restore"}

	for i, cmd := range blockedCommands {
		t.Run("BlockedCommand_"+cmd, func(t *testing.T) {
			unauthUserID := fmt.Sprintf("adversary-cmd-user-%d", i)
			dbMock.EXPECT().GetUser(unauthUserID).Return(nil, errors.New("user not found"))
			tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
				s, ok := x.(string)
				return ok && strings.Contains(s, "Unauthorized access. Please provide a valid 6-digit pairing code")
			})).Return(nil)

			err := engine.ProcessMessage(context.Background(), channel.Inbound{
				Sender:       unauthUserID,
				Conversation: channel.Conversation{ID: "chat-adv"},
				Text:         cmd,
				Channel:      "telegram",
			})
			require.NoError(t, err)
		})
	}

	t.Run("BlockedVoiceMessage", func(t *testing.T) {
		unauthUserID := "adversary-voice-user"
		dbMock.EXPECT().GetUser(unauthUserID).Return(nil, errors.New("user not found"))
		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
			s, ok := x.(string)
			return ok && strings.Contains(s, "Unauthorized access. Please provide a valid 6-digit pairing code")
		})).Return(nil)

		err := engine.ProcessMessage(context.Background(), channel.Inbound{
			Sender:       unauthUserID,
			Conversation: channel.Conversation{ID: "chat-adv"},
			Attachments:  []channel.AttachmentRef{{ID: "adversary-voice-id", Kind: channel.KindAudio, Label: "telegram_voice"}},
			Channel:      "telegram",
		})
		require.NoError(t, err)
	})

	t.Run("BlockedPhotoMessage", func(t *testing.T) {
		unauthUserID := "adversary-photo-user"
		dbMock.EXPECT().GetUser(unauthUserID).Return(nil, errors.New("user not found"))
		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
			s, ok := x.(string)
			return ok && strings.Contains(s, "Unauthorized access. Please provide a valid 6-digit pairing code")
		})).Return(nil)

		err := engine.ProcessMessage(context.Background(), channel.Inbound{
			Sender:       unauthUserID,
			Conversation: channel.Conversation{ID: "chat-adv"},
			Attachments:  []channel.AttachmentRef{{ID: "adversary-photo-id", Kind: channel.KindImage, ContentType: "image/jpeg", Label: "telegram_photo"}},
			Channel:      "telegram",
		})
		require.NoError(t, err)
	})
}

// TestAdversarial_SEC01_BruteForceLockout verifies that after 5 failed pairing attempts,
// the unauthenticated account is locked out for 1 hour.
func TestAdversarial_SEC01_BruteForceLockout(t *testing.T) {
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()

	bruteForceUserID := "adversary-brute-forcer"
	dbMock.EXPECT().GetPairingCode("000000").Return(nil, errors.New("not found")).AnyTimes()

	// Attempts 1 to 4: decreasing remaining attempts
	for attempt := 1; attempt <= 4; attempt++ {
		dbMock.EXPECT().GetUser(bruteForceUserID).Return(nil, errors.New("user not found"))
		expectedRemaining := 5 - attempt
		expectedMsg := fmt.Sprintf("(%d attempts remaining before 1-hour lockout)", expectedRemaining)
		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
			s, ok := x.(string)
			return ok && strings.Contains(s, expectedMsg)
		})).Return(nil)

		err := engine.ProcessMessage(context.Background(), channel.Inbound{
			Sender:       bruteForceUserID,
			Conversation: channel.Conversation{ID: "chat-adv"},
			Text:         "000000",
			Channel:      "telegram",
		})
		require.NoError(t, err)
	}

	// Attempt 5: triggers lockout
	dbMock.EXPECT().GetUser(bruteForceUserID).Return(nil, errors.New("user not found"))
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		s, ok := x.(string)
		return ok && strings.Contains(s, "Too many failed attempts (5/5). Your account has been locked for 1 hour.")
	})).Return(nil)

	err := engine.ProcessMessage(context.Background(), channel.Inbound{
		Sender:       bruteForceUserID,
		Conversation: channel.Conversation{ID: "chat-adv"},
		Text:         "000000",
		Channel:      "telegram",
	})
	require.NoError(t, err)

	// Attempt 6: locked out directly
	dbMock.EXPECT().GetUser(bruteForceUserID).Return(nil, errors.New("user not found"))
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		s, ok := x.(string)
		return ok && strings.Contains(s, "Too many failed pairing attempts. You are temporarily locked out.")
	})).Return(nil)

	err = engine.ProcessMessage(context.Background(), channel.Inbound{
		Sender:       bruteForceUserID,
		Conversation: channel.Conversation{ID: "chat-adv"},
		Text:         "000000",
		Channel:      "telegram",
	})
	require.NoError(t, err)
}

// TestAdversarial_SEC01_PairingCodeRedemption verifies only valid 6-digit pairing codes are redeemed.
func TestAdversarial_SEC01_PairingCodeRedemption(t *testing.T) {
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()

	unauthUserID := "adversary-pairing-user"

	// 1. 5-digit code should NOT trigger pairing code lookup
	t.Run("FiveDigitCodeRejected", func(t *testing.T) {
		dbMock.EXPECT().GetUser(unauthUserID).Return(nil, errors.New("user not found"))
		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
			s, ok := x.(string)
			return ok && strings.Contains(s, "Unauthorized access. Please provide a valid 6-digit pairing code")
		})).Return(nil)

		err := engine.ProcessMessage(context.Background(), channel.Inbound{
			Sender:       unauthUserID,
			Conversation: channel.Conversation{ID: "chat-adv"},
			Text:         "12345",
			Channel:      "telegram",
		})
		require.NoError(t, err)
	})

	// 2. 7-digit code should NOT trigger pairing code lookup
	t.Run("SevenDigitCodeRejected", func(t *testing.T) {
		dbMock.EXPECT().GetUser(unauthUserID).Return(nil, errors.New("user not found"))
		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
			s, ok := x.(string)
			return ok && strings.Contains(s, "Unauthorized access. Please provide a valid 6-digit pairing code")
		})).Return(nil)

		err := engine.ProcessMessage(context.Background(), channel.Inbound{
			Sender:       unauthUserID,
			Conversation: channel.Conversation{ID: "chat-adv"},
			Text:         "1234567",
			Channel:      "telegram",
		})
		require.NoError(t, err)
	})

	// 3. Valid 6-digit code redemption succeeds
	t.Run("ValidSixDigitCodeRedeemed", func(t *testing.T) {
		validCode := "654321"
		dbMock.EXPECT().GetUser(unauthUserID).Return(nil, errors.New("user not found"))
		dbMock.EXPECT().GetPairingCode(validCode).Return(&models.PairingCode{
			ID:          "pc-1",
			Code:        validCode,
			Role:        "family",
			FamilyGroup: "AdversaryFamily",
			ExpiresAt:   time.Now().Add(10 * time.Minute),
		}, nil)
		dbMock.EXPECT().RedeemPairingCode("pc-1", gomock.Cond(func(x any) bool {
			u, ok := x.(models.User)
			return ok && u.PlatformID == unauthUserID && u.Role == "family" && u.FamilyGroup == "AdversaryFamily"
		})).Return(nil)

		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
			s, ok := x.(string)
			return ok && strings.Contains(s, "Pairing successful!")
		})).Return(nil)

		err := engine.ProcessMessage(context.Background(), channel.Inbound{
			Sender:       unauthUserID,
			Conversation: channel.Conversation{ID: "chat-adv"},
			Text:         validCode,
			Channel:      "telegram",
		})
		require.NoError(t, err)
	})

	// 4. Valid /start 6-digit code redemption succeeds
	t.Run("ValidStartPrefixedCodeRedeemed", func(t *testing.T) {
		validCode := "987654"
		dbMock.EXPECT().GetUser(unauthUserID).Return(nil, errors.New("user not found"))
		dbMock.EXPECT().GetPairingCode(validCode).Return(&models.PairingCode{
			ID:          "pc-2",
			Code:        validCode,
			Role:        "admin",
			FamilyGroup: "AdminFamily",
			ExpiresAt:   time.Now().Add(10 * time.Minute),
		}, nil)
		dbMock.EXPECT().RedeemPairingCode("pc-2", gomock.Cond(func(x any) bool {
			u, ok := x.(models.User)
			return ok && u.PlatformID == unauthUserID && u.Role == "admin"
		})).Return(nil)

		tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
			s, ok := x.(string)
			return ok && strings.Contains(s, "Pairing successful!")
		})).Return(nil)

		err := engine.ProcessMessage(context.Background(), channel.Inbound{
			Sender:       unauthUserID,
			Conversation: channel.Conversation{ID: "chat-adv"},
			Text:         "/start " + validCode,
			Channel:      "telegram",
		})
		require.NoError(t, err)
	})
}

// TestAdversarial_SEC01_RBAC_NonAdminRolesDenied verifies that non-admin roles (family, guest, etc.)
// receive permission denied on /backup and /restore.
func TestAdversarial_SEC01_RBAC_NonAdminRolesDenied(t *testing.T) {
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()

	nonAdminRoles := []string{"family", "guest", "member", "child"}

	for _, role := range nonAdminRoles {
		t.Run("Role_"+role, func(t *testing.T) {
			userID := "user-" + role
			user := &models.User{
				PlatformID:  userID,
				Role:        role,
				FamilyGroup: "TestFamily",
			}

			// /backup denied
			dbMock.EXPECT().GetUser(userID).Return(user, nil)
			tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
				s, ok := x.(string)
				return ok && strings.Contains(s, "Permission denied: administrative privileges required")
			})).Return(nil)

			err := engine.ProcessMessage(context.Background(), channel.Inbound{
				Sender:       userID,
				Conversation: channel.Conversation{ID: "chat-adv"},
				Text:         "/backup",
				Channel:      "telegram",
			})
			require.NoError(t, err)

			// /restore denied
			dbMock.EXPECT().GetUser(userID).Return(user, nil)
			tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
				s, ok := x.(string)
				return ok && strings.Contains(s, "Permission denied: administrative privileges required")
			})).Return(nil)

			err = engine.ProcessMessage(context.Background(), channel.Inbound{
				Sender:       userID,
				Conversation: channel.Conversation{ID: "chat-adv"},
				Text:         "/restore",
				Channel:      "telegram",
			})
			require.NoError(t, err)
		})
	}
}
