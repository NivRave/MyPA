package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// fakeHealthReporter records who asked for a report.
type fakeHealthReporter struct {
	askedBy []*models.User
	report  string
}

func (f *fakeHealthReporter) Report(_ context.Context, admin *models.User) string {
	f.askedBy = append(f.askedBy, admin)
	return f.report
}

// doctorEngine returns an engine whose only user is `user`, and the texts it sends.
func doctorEngine(t *testing.T, user models.User) (*Engine, *[]string, func()) {
	t.Helper()
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	inHousehold(&user)
	dbMock.EXPECT().GetUser(user.PlatformID).Return(&user, nil).AnyTimes()
	sent := &[]string{}
	tgMock.EXPECT().SendMessage(gomock.Any(), "chat-1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, text string) error {
			*sent = append(*sent, text)
			return nil
		}).AnyTimes()
	return engine, sent, cleanup
}

func doctorMessage(userID string) channel.Inbound {
	return channel.Inbound{MessageID: "m-1", Sender: userID, Conversation: channel.Conversation{ID: "chat-1"}, Text: "/doctor", Channel: "telegram"}
}

// V4-17: /doctor is an admin command and sends the health report.
func TestDoctorCommand_AdminGetsTheReport(t *testing.T) {
	admin := models.User{PlatformID: "1001", Name: "Dana", Role: "admin", FamilyGroup: "Levi"}
	engine, sent, cleanup := doctorEngine(t, admin)
	defer cleanup()
	reporter := &fakeHealthReporter{report: "🩺 **MyPA health check**\nAll 6 checks passed."}
	engine.WithHealthReporter(reporter)

	require.NoError(t, engine.ProcessMessage(context.Background(), doctorMessage("1001")))

	require.Len(t, reporter.askedBy, 1)
	assert.Equal(t, "1001", reporter.askedBy[0].PlatformID)
	assert.Equal(t, "Levi", reporter.askedBy[0].FamilyGroup, "the report is scoped by the caller's own record")
	require.Len(t, *sent, 1)
	assert.Contains(t, (*sent)[0], "MyPA health check")
}

// A family member must not learn anything about the install's health or other members' accounts.
func TestDoctorCommand_NonAdminIsRefused(t *testing.T) {
	member := models.User{PlatformID: "1002", Name: "Omer", Role: "family", FamilyGroup: "Levi"}
	engine, sent, cleanup := doctorEngine(t, member)
	defer cleanup()
	reporter := &fakeHealthReporter{report: "secret health details"}
	engine.WithHealthReporter(reporter)

	require.NoError(t, engine.ProcessMessage(context.Background(), doctorMessage("1002")))

	assert.Empty(t, reporter.askedBy, "the checks must not even run for a non-admin")
	require.Len(t, *sent, 1)
	assert.Contains(t, (*sent)[0], "Permission denied")
	assert.NotContains(t, (*sent)[0], "secret health details")
}

func TestDoctorCommand_WithoutAReporterSaysSo(t *testing.T) {
	admin := models.User{PlatformID: "1001", Name: "Dana", Role: "admin"}
	engine, sent, cleanup := doctorEngine(t, admin)
	defer cleanup()

	require.NoError(t, engine.ProcessMessage(context.Background(), doctorMessage("1001")))

	require.Len(t, *sent, 1)
	assert.Contains(t, (*sent)[0], "not available")
}

// An unpaired sender never reaches the command: the pre-auth gate answers first.
func TestDoctorCommand_UnpairedSenderNeverReachesIt(t *testing.T) {
	engine, tgMock, dbMock, cleanup := setupUnauthTestEngine(t)
	defer cleanup()
	dbMock.EXPECT().GetUser("666").Return(nil, assert.AnError)
	var sent []string
	tgMock.EXPECT().SendMessage(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, text string) error { sent = append(sent, text); return nil }).AnyTimes()
	reporter := &fakeHealthReporter{report: "secret health details"}
	engine.WithHealthReporter(reporter)

	msg := doctorMessage("666")
	require.NoError(t, engine.ProcessMessage(context.Background(), msg))

	assert.Empty(t, reporter.askedBy)
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0], "Unauthorized access")
}

func TestHelp_ListsDoctor(t *testing.T) {
	admin := models.User{PlatformID: "1001", Name: "Dana", Role: "admin"}
	engine, sent, cleanup := doctorEngine(t, admin)
	defer cleanup()

	msg := doctorMessage("1001")
	msg.Text = "/help"
	require.NoError(t, engine.ProcessMessage(context.Background(), msg))

	require.NotEmpty(t, *sent)
	assert.True(t, strings.Contains(strings.Join(*sent, "\n"), "/doctor"), "the help text must mention /doctor")
}
