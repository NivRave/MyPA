package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/google/uuid"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/testutil/householdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V4-35 done-when, end to end: each person signs in the production way (a sign-in token becomes a
// session), every request goes through the real middleware and the generated schema, and roles
// are the memberships in the database. Each role sees and changes only what it may (ADR 0002
// Decision 1, and the owner's decision of 2026-11-20 that an owner sees children's and guests'
// activity only, as an adult's).

type gqlResult struct {
	status int
	data   map[string]any
	errs   []string
}

func (r gqlResult) err() string { return strings.Join(r.errs, "; ") }

// dashboardServer is /graphql as the app serves it.
func dashboardServer(c *db.Client) http.Handler {
	srv := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: NewResolver(ResolverConfig{DB: c})}))
	return NewAuthMiddleware(c, c)(srv)
}

func gql(t *testing.T, h http.Handler, secret, query string, vars map[string]any) gqlResult {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	out := gqlResult{status: rr.Code}
	var resp struct {
		Data   map[string]any `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	out.data = resp.Data
	for _, e := range resp.Errors {
		out.errs = append(out.errs, e.Message)
	}
	return out
}

// signInAs gives a member a dashboard session the way /dashboard and the sign-in page do.
func signInAs(t *testing.T, c *db.Client, u models.User) string {
	t.Helper()
	ctx := context.Background()
	s, err := identity.ForUser(ctx, c, &u)
	require.NoError(t, err)
	token, err := identity.NewSecret()
	require.NoError(t, err)
	_, err = c.IssueSignIn(ctx, s, identity.HashSecret(token))
	require.NoError(t, err)
	secret, err := identity.NewSecret()
	require.NoError(t, err)
	_, _, err = c.RedeemSignIn(ctx, identity.HashSecret(token), identity.HashSecret(secret))
	require.NoError(t, err)
	return secret
}

func TestSessionAuth_EachRoleSeesAndChangesOnlyWhatItMay(t *testing.T) {
	resolver, _, cleanup := setupTestResolver(t)
	defer cleanup()
	c := resolver.DB
	h := dashboardServer(c)
	ctx := context.Background()
	levi := "Levi-" + uuid.NewString()
	cohen := "Cohen-" + uuid.NewString()

	dana := householdtest.Member(t, c, "1001-"+levi, "Dana", "admin", levi, "telegram")  // owner and operator
	omer := householdtest.Member(t, c, "1002-"+levi, "Omer", "family", levi, "telegram") // adult
	noa := householdtest.Member(t, c, "1003-"+levi, "Noa", "adult", levi, "telegram")    // adult
	kid := householdtest.Member(t, c, "1004-"+levi, "Kid", "child", levi, "telegram")
	sitter := householdtest.Member(t, c, "1005-"+levi, "Sitter", "guest", levi, "telegram")
	other := householdtest.Member(t, c, "2001-"+cohen, "Ran", "admin", cohen, "telegram")

	S := map[string]string{}
	for name, u := range map[string]models.User{"dana": dana, "omer": omer, "noa": noa, "kid": kid, "sitter": sitter, "other": other} {
		S[name] = signInAs(t, c, u)
	}

	// --- Who may use the dashboard at all: owners and adults; never children or guests. ---
	for name, want := range map[string]string{"dana": "owner", "omer": "adult", "noa": "adult"} {
		r := gql(t, h, S[name], `{ me { personId role isOperator household { id } } }`, nil)
		require.Empty(t, r.errs, name)
		me := r.data["me"].(map[string]any)
		assert.Equal(t, want, me["role"], name)
		assert.Equal(t, dana.HouseholdID, me["household"].(map[string]any)["id"], name)
	}
	assert.Equal(t, true, gql(t, h, S["dana"], `{ me { isOperator } }`, nil).data["me"].(map[string]any)["isOperator"])
	for _, name := range []string{"kid", "sitter"} {
		for _, q := range []string{`{ me { role } }`, `{ household { id } }`, `{ babyProfiles { id } }`, `{ topicMemories { id } }`} {
			r := gql(t, h, S[name], q, nil)
			assert.Contains(t, r.err(), "owners and adults", "%s: %s", name, q)
		}
	}

	// --- Owner-only: settings, members and roles, pairing codes. ---
	r := gql(t, h, S["omer"], `mutation { updateHousehold(name: "Taken over") { id } }`, nil)
	assert.Contains(t, r.err(), "only an owner", "an adult may not change the household's settings")
	r = gql(t, h, S["dana"], `mutation { updateHousehold(name: "The Levis") { name } }`, nil)
	require.Empty(t, r.errs)
	assert.Equal(t, "The Levis", r.data["updateHousehold"].(map[string]any)["name"])

	r = gql(t, h, S["dana"], `{ users { id role } }`, nil)
	require.Empty(t, r.errs)
	roles := map[string]string{}
	for _, u := range r.data["users"].([]any) {
		m := u.(map[string]any)
		roles[m["id"].(string)] = m["role"].(string)
	}
	assert.Equal(t, map[string]string{dana.ID: "owner", omer.ID: "adult", noa.ID: "adult", kid.ID: "child", sitter.ID: "guest"}, roles,
		"an owner lists the household's members with their roles, and nobody else's (BL-31)")
	r = gql(t, h, S["omer"], `{ users { id } }`, nil)
	require.Empty(t, r.errs)
	require.Len(t, r.data["users"], 1, "an adult sees only themselves")
	assert.Equal(t, omer.ID, r.data["users"].([]any)[0].(map[string]any)["id"])

	for _, q := range []string{`{ activePairingCodes { id } }`, `mutation { generatePairingCode(role: "adult") { id } }`} {
		assert.Contains(t, gql(t, h, S["omer"], q, nil).err(), "only an owner", q)
	}
	r = gql(t, h, S["dana"], `mutation { generatePairingCode(role: "child") { role code } }`, nil)
	require.Empty(t, r.errs)
	assert.Equal(t, "child", r.data["generatePairingCode"].(map[string]any)["role"])
	for _, role := range []string{"owner", "admin", "operator"} {
		r = gql(t, h, S["dana"], `mutation($r: String!) { generatePairingCode(role: $r) { id } }`, map[string]any{"r": role})
		assert.Contains(t, r.err(), "invalid role", "a pairing code never makes an %s", role)
	}

	// --- Shared household data: owners and adults read and change it. ---
	r = gql(t, h, S["omer"], `mutation { upsertBabyProfile(name: "Yael", dateOfBirth: "2026-01-01T00:00:00Z", gender: "female") { id } }`, nil)
	require.Empty(t, r.errs)
	for _, name := range []string{"dana", "noa"} {
		r = gql(t, h, S[name], `{ babyProfiles { name } }`, nil)
		require.Empty(t, r.errs, name)
		require.Len(t, r.data["babyProfiles"], 1, name)
	}
	r = gql(t, h, S["other"], `{ babyProfiles { name } }`, nil)
	require.Empty(t, r.errs)
	assert.Empty(t, r.data["babyProfiles"], "another household's owner sees none of it")

	// --- Personal data: only its person sees and changes it, an owner included. ---
	r = gql(t, h, S["noa"], `mutation { saveTopicMemory(topic: "health", subtopic: "private", fact: "Noa private fact", scope: "personal") { id } }`, nil)
	require.Empty(t, r.errs)
	memID := r.data["saveTopicMemory"].(map[string]any)["id"].(string)
	r = gql(t, h, S["noa"], `mutation { logFinancialTransaction(type: "expense", category: "health", merchant: "Noa clinic", amount: 80, currency: "ILS", isShared: false) { id } }`, nil)
	require.Empty(t, r.errs)
	// isShared: false is stored as true today (the model's default:true drops a false on insert,
	// BL-43, chat included); store it as the person asked, so the visibility rule is what is tested.
	require.NoError(t, c.DB.Model(&models.FinancialTransaction{}).Where("merchant = ?", "Noa clinic").Update("is_shared", false).Error)
	r = gql(t, h, S["noa"], `mutation { createScheduleEvent(topic: "health", title: "Noa appointment", startTime: "2026-12-01T10:00:00Z", endTime: "2026-12-01T11:00:00Z", isShared: false) { id } }`, nil)
	require.Empty(t, r.errs)
	eventID := r.data["createScheduleEvent"].(map[string]any)["id"].(string)
	r = gql(t, h, S["noa"], `mutation($u: ID!) { saveUserPreference(userId: $u, standingInstructions: "Noa standing order") { userId } }`, map[string]any{"u": noa.ID})
	require.Empty(t, r.errs)

	personal := `{ topicMemories { fact } financialTransactions { merchant } scheduleEvents { title } }`
	r = gql(t, h, S["noa"], personal, nil)
	require.Empty(t, r.errs)
	got, _ := json.Marshal(r.data)
	for _, s := range []string{"Noa private fact", "Noa clinic", "Noa appointment"} {
		assert.Contains(t, string(got), s, "the person sees their own personal data")
	}
	for _, name := range []string{"dana", "omer"} {
		r = gql(t, h, S[name], personal, nil)
		require.Empty(t, r.errs, name)
		got, _ = json.Marshal(r.data)
		for _, s := range []string{"Noa private fact", "Noa clinic", "Noa appointment"} {
			assert.NotContains(t, string(got), s, "%s must not see another adult's personal data", name)
		}
		r = gql(t, h, S[name], `query($u: ID!) { userPreference(userId: $u) { standingInstructions } }`, map[string]any{"u": noa.ID})
		require.Empty(t, r.errs, name)
		got, _ = json.Marshal(r.data)
		assert.NotContains(t, string(got), "Noa standing order", name)

		for _, q := range []string{`mutation($id: ID!) { deleteTopicMemory(id: $id) }`, `mutation($id: ID!) { pinTopicMemory(id: $id, isPinned: true) { id } }`} {
			assert.NotEmpty(t, gql(t, h, S[name], q, map[string]any{"id": memID}).errs, "%s may not change another adult's memory", name)
		}
		assert.NotEmpty(t, gql(t, h, S[name], `mutation($id: ID!) { deleteScheduleEvent(id: $id) }`, map[string]any{"id": eventID}).errs, name)
	}
	r = gql(t, h, S["noa"], `mutation($id: ID!) { pinTopicMemory(id: $id, isPinned: true) { isPinned } }`, map[string]any{"id": memID})
	require.Empty(t, r.errs, "the person changes their own: %s", r.err())

	// --- Activity: an owner sees each member's activity without the text; an adult only their own. ---
	noaLog := models.MutationLog{ID: uuid.NewString(), UserID: noa.PlatformID, HouseholdID: noa.HouseholdID, FamilyGroup: levi, Topic: "health",
		Action: "create", EntityType: "topic_memory", EntityID: memID, BeforeJSON: "{}", AfterJSON: `{"fact":"Noa private fact"}`, Summary: "Saved Noa private fact", CreatedAt: time.Now()}
	require.NoError(t, c.DB.Create(&noaLog).Error)
	for _, m := range []models.User{noa, kid} {
		require.NoError(t, c.DB.Create(&models.AuditSession{ID: uuid.NewString(), UserID: m.PlatformID, Source: "telegram",
			UserPrompt: m.Name + " secret prompt", Status: "success", StartTime: time.Now()}).Error)
	}

	r = gql(t, h, S["dana"], `{ mutationLogs { id userId summary afterJson } auditSessions { userId userPrompt } userSessions { userId } }`, nil)
	require.Empty(t, r.errs)
	var sawLog bool
	for _, l := range r.data["mutationLogs"].([]any) {
		m := l.(map[string]any)
		if m["id"] == noaLog.ID {
			sawLog = true
			assert.Equal(t, "create topic_memory", m["summary"], "who, when and what kind, not what")
			assert.Nil(t, m["afterJson"])
		}
	}
	assert.True(t, sawLog, "an owner sees a member's activity")
	prompts := map[string]string{}
	for _, s := range r.data["auditSessions"].([]any) {
		m := s.(map[string]any)
		prompts[m["userId"].(string)] = m["userPrompt"].(string)
	}
	require.Contains(t, prompts, noa.PlatformID)
	require.Contains(t, prompts, kid.PlatformID)
	assert.Empty(t, prompts[noa.PlatformID], "an owner never reads another adult's chats")
	assert.Empty(t, prompts[kid.PlatformID], "nor a child's: activity only (owner decision, 2026-11-20)")
	assert.Len(t, r.data["userSessions"], 2)

	r = gql(t, h, S["omer"], `{ mutationLogs { id } auditSessions { userId } userSessions { userId } }`, nil)
	require.Empty(t, r.errs)
	assert.Empty(t, r.data["mutationLogs"], "an adult sees no one else's activity")
	assert.Empty(t, r.data["auditSessions"])
	assert.Empty(t, r.data["userSessions"])

	r = gql(t, h, S["noa"], `{ mutationLogs { id summary } auditSessions { userPrompt } }`, nil)
	require.Empty(t, r.errs)
	got, _ = json.Marshal(r.data)
	assert.Contains(t, string(got), "Saved Noa private fact", "the person sees their own activity in full")
	assert.Contains(t, string(got), "Noa secret prompt")

	// --- Undo: one's own changes only. ---
	r = gql(t, h, S["dana"], `mutation($id: ID!) { rollbackMutation(mutationId: $id) }`, map[string]any{"id": noaLog.ID})
	assert.Contains(t, r.err(), "permission denied", "an owner may not undo another member's change")

	// --- A demotion or a removal applies to the next request; signing out ends the session. ---
	require.NoError(t, c.DB.Model(&models.Membership{}).Where("person_id = ?", noa.ID).Update("role", "child").Error)
	assert.Contains(t, gql(t, h, S["noa"], `{ me { role } }`, nil).err(), "owners and adults")
	require.NoError(t, c.DB.Model(&models.Membership{}).Where("person_id = ?", noa.ID).Update("role", "adult").Error)
	assert.Empty(t, gql(t, h, S["noa"], `{ me { role } }`, nil).errs)

	r = gql(t, h, S["omer"], `mutation { signOut }`, nil)
	require.Empty(t, r.errs)
	assert.Equal(t, true, r.data["signOut"])
	assert.Equal(t, http.StatusUnauthorized, gql(t, h, S["omer"], `{ me { role } }`, nil).status, "a signed-out session is refused")

	n, err := c.RevokeSessionsOf(ctx, dana.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.Equal(t, http.StatusUnauthorized, gql(t, h, S["dana"], `{ me { role } }`, nil).status, "/signout ends the session")

	// --- No session, no access. ---
	assert.Equal(t, http.StatusUnauthorized, gql(t, h, "", `{ household { id } }`, nil).status)
}
