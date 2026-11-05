package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestProcessMessage_PreAuthGate_DisallowsBackupAndRestore(t *testing.T) {
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()

	unauthUserID := "unauth-user-999"

	// 1. /backup from unauthenticated user
	dbMock.EXPECT().GetUser(unauthUserID).Return(nil, errors.New("user not found"))
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		s, ok := x.(string)
		return ok && strings.Contains(s, "Unauthorized access. Please provide a valid 6-digit pairing code")
	})).Return(nil)

	err := engine.ProcessMessage(context.Background(), models.Message{
		UserID: unauthUserID,
		ChatID: "chat-999",
		Text:   "/backup",
		Source: "telegram",
	})
	require.NoError(t, err)

	// 2. /restore from unauthenticated user
	dbMock.EXPECT().GetUser(unauthUserID).Return(nil, errors.New("user not found"))
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		s, ok := x.(string)
		return ok && strings.Contains(s, "Unauthorized access. Please provide a valid 6-digit pairing code")
	})).Return(nil)

	err = engine.ProcessMessage(context.Background(), models.Message{
		UserID: unauthUserID,
		ChatID: "chat-999",
		Text:   "/restore",
		Source: "telegram",
	})
	require.NoError(t, err)
}

func TestProcessMessage_PreAuthGate_DisallowsMediaDownloads(t *testing.T) {
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()

	unauthUserID := "unauth-media-user"

	dbMock.EXPECT().GetUser(unauthUserID).Return(nil, errors.New("user not found"))
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		s, ok := x.(string)
		return ok && strings.Contains(s, "Unauthorized access")
	})).Return(nil)

	err := engine.ProcessMessage(context.Background(), models.Message{
		UserID:      unauthUserID,
		ChatID:      "chat-media",
		PhotoFileID: "malicious-photo-file-id",
		Source:      "telegram",
	})
	require.NoError(t, err)
}

func TestProcessMessage_AdminRBAC_FamilyRoleRejected(t *testing.T) {
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()

	familyUserID := "family-user-123"
	familyUser := &models.User{
		PlatformID:  familyUserID,
		Role:        "family",
		FamilyGroup: "TestFamily",
	}

	// 1. /backup from family member rejected
	dbMock.EXPECT().GetUser(familyUserID).Return(familyUser, nil)
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		s, ok := x.(string)
		return ok && strings.Contains(s, "Permission denied: administrative privileges required")
	})).Return(nil)

	err := engine.ProcessMessage(context.Background(), models.Message{
		UserID: familyUserID,
		ChatID: "chat-123",
		Text:   "/backup",
		Source: "telegram",
	})
	require.NoError(t, err)

	// 2. /restore from family member rejected
	dbMock.EXPECT().GetUser(familyUserID).Return(familyUser, nil)
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Cond(func(x any) bool {
		s, ok := x.(string)
		return ok && strings.Contains(s, "Permission denied: administrative privileges required")
	})).Return(nil)

	err = engine.ProcessMessage(context.Background(), models.Message{
		UserID: familyUserID,
		ChatID: "chat-123",
		Text:   "/restore",
		Source: "telegram",
	})
	require.NoError(t, err)
}
