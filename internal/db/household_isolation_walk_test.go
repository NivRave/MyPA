package db

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/models"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Markers that only household A's rows carry. A result that contains one of them leaked A's data.
const (
	markerText   = "secret-of-a"
	markerNumber = 4242
)

// unscopedMethods are the repository methods that take no identity.Scope, each with the reason.
// The walk fails on an unscoped method that is not listed: a new method must either take a scope
// or be classified here (ADR 0002 Decision 2: coverage must not depend on someone remembering).
var unscopedMethods = map[string]string{
	// Connection and helpers.
	"Ping": "infrastructure", "Close": "infrastructure", "MemoryV2": "returns the memory repository",
	"BootstrapHouseholds": "fresh-install seeding, only when no household exists (Decision 6)",
	// Households and identity: the tenant itself, and the sources a scope is built from.
	"HouseholdByID": "identity.Store", "HouseholdsByLabel": "identity.Store", "CountHouseholds": "install-level count",
	"HouseholdPacks":  "a scheduled job reads the packs of the household its stored row names (V4-27)",
	"QuietSettings":   "person-keyed: a scheduled job reads the time zone and quiet hours of the person its row names (V4-05)",
	"CreateHousehold": "creates a tenant", "GetUser": "the sender lookup a scope is built from",
	"UpsertUser": "bootstrap only, with an explicit household", "GetPairingCode": "an unauthenticated sender redeeming a code",
	"RedeemPairingCode": "takes the household from the code row",
	// Scheduled jobs: scans of every household; each row's household_id becomes the scope that acts on it.
	"GetDueSharedReminders": "scheduled scan", "ListActiveAutomationRules": "scheduled scan",
	"ListActiveMemoriesForConsolidation": "scheduled scan",
	// Person-keyed tables (no household_id; see personKeyedTables).
	"InsertAuditSession": "person-keyed", "InsertAuditEvent": "person-keyed", "GetUniqueUsers": "person-keyed",
	"GetLastAuditSessionForUser": "person-keyed", "SaveReminder": "person-keyed", "GetDueReminders": "person-keyed scan",
	"MarkReminderSent": "person-keyed", "SaveWorkflow": "person-keyed", "GetActiveWorkflows": "person-keyed scan",
	"GetUserWorkflows": "person-keyed", "DeleteWorkflow": "person-keyed",
	"SaveEncryptedCredential": "person-keyed", "GetEncryptedCredential": "person-keyed",
	"CheckIdempotency": "person-keyed", "SaveIdempotency": "person-keyed",
	"UpsertUserPreference": "person-keyed", "GetUserPreference": "person-keyed",
	"UpsertTopicCalendarConfig": "person-keyed", "GetTopicCalendarConfig": "person-keyed",
	"ListTopicCalendarConfigs": "person-keyed",
	// People and channel accounts (V4-32): keyed by a verified account or person, never by a
	// household argument; the household of a code comes from its row.
	"UserForAccount": "the sender lookup a scope is built from", "AccountOf": "a verified sender's own account",
	"RecordInbound": "a verified sender's own account", "PersonOfAccount": "the processing lane of a verified sender",
	"PersonOfKey": "maps an old person key to its person", "PersonByID": "the acting person's own row",
	"AccountsOfPerson": "the acting person's own accounts", "DeliveryAccount": "where the person behind a stored key is messaged",
	"AccountKeysOf":          "the person's own Google sign-in keys",
	"SetPreferredAccount":    "only one of the person's own accounts (checked in the statement)",
	"RemoveAccount":          "only accounts of the acting account's own person",
	"ClaimLinkCode":          "an unauthenticated sender redeeming a code; the household comes from the code row",
	"PendingLink":            "the issuing account's own code",
	"ConfirmLink":            "only from the issuing account; the person comes from the code row",
	"MergeOnLink":            "only from the account that typed the code, after the issuer confirmed, inside the code's household",
	"ConfirmMergeByIssuer":   "only from the issuing account; merges nothing on its own",
	"PendingMerge":           "the account that typed the code, its own pending merge",
	"ResolveAccountChannels": "start-up: names the channel of accounts the upgrade made, by their ID",
	// Roles and dashboard sign-in (V4-35): keyed by a verified person or a secret's hash; the
	// household of a session is read from the person's membership on every request.
	"AccessOf":               "identity.Store: the membership and person a scope is built from",
	"UserByID":               "the person of a verified session, a scope is built from it",
	"RedeemSignIn":           "a browser presenting a sign-in token; the person comes from the token row",
	"SessionPerson":          "a browser presenting a session secret; a scope is built from its person",
	"RevokeSession":          "the acting session itself",
	"RevokeSessionsOf":       "the acting person's own sessions (/signout)",
	"PurgeSignIns":           "install-level clean-up of ended sessions and used links",
	"HouseholdsWithoutOwner": "install-level count for /doctor",
	"GrantAccess":            "offline operator recovery command, never moves a person between households",
	// V4-33: proactive delivery rows are one person's, written and read by the delivery policy for
	// the person a reminder, briefing or notification is for, never on a member's request.
	"CreateDelivery":    "the delivery policy records a message for the person it is for",
	"SaveDelivery":      "the delivery policy updates the row it holds",
	"ClaimDelivery":     "the delivery policy claims one row by its ID",
	"HeldDeliveries":    "the held messages of the person who just wrote, sent to that person",
	"DueDeliveries":     "install-level minute tick: retries of messages already recorded",
	"DeliveryByRef":     "a platform's signed delivery report, matched by the platform's message ID",
	"ExpireDeliveries":  "install-level minute tick: expiry of held messages",
	"UnstickDeliveries": "install-level minute tick: rows a crash left mid-send",
	"PurgeDeliveries":   "install-level clean-up of messages that ended a month ago",
	"DeliveryCounts":    "/doctor counts for the admin's own household, by its memberships",
	// V4-03: the embedding model is install-wide; every household's vectors must be in one space.
	"EmbeddingStatus":     "install-level counts for the offline `mypa embeddings status`",
	"CheckEmbeddingSpace": "install-level start-up check of the recorded embedding model; changes no memory",
	"Reembed":             "offline `mypa embeddings reembed`: every household's vectors move to one model together",
}

var scopeType = reflect.TypeOf(identity.Scope{})

// repositories returns every repository type the walk covers, bound to db.
func repositories(db *gorm.DB) map[string]any {
	c := &Client{DB: db}
	return map[string]any{"Client": c, "SharedRepository": NewSharedRepository(db), "MemoryV2Repo": c.MemoryV2()}
}

// seedHouseholdA writes one row of household A into every household table, every text column
// carrying markerText and every number markerNumber (IDs referring to other rows are 1, the ID
// of A's row in that table on a fresh database).
func seedHouseholdA(t *testing.T, c *Client, a models.Household) {
	t.Helper()
	overrides := map[string]map[string]any{
		"topic_memories":         {"scope": "family", "superseded_by_id": nil, "valid_until": nil},
		"automation_rules":       {"is_active": true},
		"baby_care_logs":         {"log_type": "feeding"},
		"financial_transactions": {"type": "expense"},
		"schedule_events":        {"is_shared": true},
		"shared_spaces":          {"shared_with_user_id": "*", "can_view": true},
		"shared_task_reminders":  {"is_completed": false, "is_sent": false},
		"pairing_codes":          {"is_used": false, "expires_at": time.Now().Add(time.Hour)},
		"mutation_logs":          {"id": markerText, "is_undone": false, "before_json": `{"m":"` + markerText + `"}`, "after_json": `{"m":"` + markerText + `"}`},
	}
	for _, table := range labelledTables {
		rows, err := c.DB.Raw(`SELECT column_name, udt_name FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = ?`, table).Rows()
		require.NoError(t, err)
		values := map[string]any{}
		for rows.Next() {
			var col, udt string
			require.NoError(t, rows.Scan(&col, &udt))
			switch {
			case col == "household_id":
				values[col] = a.ID
			case col == "family_group":
				values[col] = *a.LegacyLabel
			case col == "id" && (udt == "int4" || udt == "int8"):
				// serial
			case col == "id" && udt == "uuid":
				// default
			case udt == "uuid" && strings.HasSuffix(col, "person_id"):
				// filled by the database from the old person key (V4-32)
			case udt == "text" || udt == "varchar":
				values[col] = markerText
			case udt == "int2" || udt == "int4" || udt == "int8":
				if strings.HasSuffix(col, "_id") {
					values[col] = 1
				} else {
					values[col] = markerNumber
				}
			case udt == "numeric" || udt == "float4" || udt == "float8":
				values[col] = markerNumber
			case udt == "bool":
				values[col] = false
			case udt == "timestamptz" || udt == "timestamp" || udt == "date":
				values[col] = time.Now()
			case udt == "jsonb" || udt == "json":
				values[col] = `{"m":"` + markerText + `"}`
			case udt == "vector":
				values[col] = pgvector.NewVector(make([]float32, 3072))
			case udt == "uuid":
				values[col] = a.ID // any other uuid column
			}
		}
		require.NoError(t, rows.Close())
		for k, v := range overrides[table] {
			values[k] = v
		}
		require.NoError(t, c.DB.Table(table).Create(values).Error, "seed %s", table)
	}
}

// snapshotA returns household A's rows of every household table, in a stable form.
func snapshotA(t *testing.T, db *gorm.DB, householdID string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range labelledTables {
		var s string
		require.NoError(t, db.Raw(fmt.Sprintf(`SELECT COALESCE(string_agg(r::text, '|' ORDER BY r::text), '')
			FROM (SELECT row_to_json(x) AS r FROM public.%s x WHERE household_id = ?) q`, table), householdID).Scan(&s).Error)
		out[table] = s
	}
	return out
}

// removeHousehold deletes every row of the household from the household tables.
func removeHousehold(t *testing.T, db *gorm.DB, householdID string) {
	t.Helper()
	for _, table := range labelledTables {
		require.NoError(t, db.Exec(fmt.Sprintf(`DELETE FROM public.%s WHERE household_id = ?`, table), householdID).Error)
	}
}

// argFor builds an argument of type t aimed at household A's rows.
func argFor(t reflect.Type, scope identity.Scope, a models.Household, timeIdx *int) reflect.Value {
	switch {
	case t == scopeType:
		return reflect.ValueOf(scope)
	case t.Implements(reflect.TypeOf((*context.Context)(nil)).Elem()):
		return reflect.ValueOf(context.Background())
	case t == reflect.TypeOf(time.Time{}):
		*timeIdx++
		if *timeIdx%2 == 1 {
			return reflect.ValueOf(time.Now().AddDate(-100, 0, 0))
		}
		return reflect.ValueOf(time.Now().AddDate(100, 0, 0))
	case t == reflect.TypeOf(pgvector.Vector{}):
		return reflect.ValueOf(pgvector.NewVector(make([]float32, 3072)))
	}
	switch t.Kind() {
	case reflect.String:
		return reflect.ValueOf(markerText).Convert(t)
	case reflect.Uint, reflect.Uint32, reflect.Uint64, reflect.Int, reflect.Int32, reflect.Int64:
		return reflect.ValueOf(1).Convert(t)
	case reflect.Float32, reflect.Float64:
		return reflect.ValueOf(1.0).Convert(t)
	case reflect.Pointer:
		if t.Elem().Kind() == reflect.Struct && t.Elem() != reflect.TypeOf(time.Time{}) {
			v := reflect.New(t.Elem())
			fillStruct(v.Elem(), a)
			return v
		}
	case reflect.Struct:
		v := reflect.New(t).Elem()
		fillStruct(v, a)
		return v
	}
	return reflect.Zero(t)
}

// fillStruct aims a model at A's row: ID 1, A's household ID and label (a write must ignore both),
// and A's marker text in every string field.
func fillStruct(v reflect.Value, a models.Household) {
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		name := v.Type().Field(i).Name
		if !f.CanSet() {
			continue
		}
		switch {
		case name == "HouseholdID":
			f.SetString(a.ID)
		case name == "FamilyGroup":
			f.SetString(*a.LegacyLabel)
		case name == "ID" && f.Kind() == reflect.String:
			f.SetString(markerText)
		case name == "ID" && (f.Kind() == reflect.Uint || f.Kind() == reflect.Int):
			f.Set(reflect.ValueOf(1).Convert(f.Type()))
		case f.Kind() == reflect.String:
			f.SetString(markerText)
		case f.Type() == reflect.TypeOf(time.Time{}):
			f.Set(reflect.ValueOf(time.Now()))
		case f.Type() == reflect.TypeOf(pgvector.Vector{}):
			f.Set(reflect.ValueOf(pgvector.NewVector(make([]float32, 3072))))
		}
	}
}

func takesScope(m reflect.Method) bool {
	for i := 1; i < m.Type.NumIn(); i++ {
		if m.Type.In(i) == scopeType {
			return true
		}
	}
	return false
}

// call invokes m on recv with arguments aimed at A, under scope; it reports the results as JSON
// and the returned error (a panic counts as an error).
func call(recv any, m reflect.Method, scope identity.Scope, a models.Household) (results string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	timeIdx := 0
	args := []reflect.Value{reflect.ValueOf(recv)}
	for i := 1; i < m.Type.NumIn(); i++ {
		in := m.Type.In(i)
		if m.Type.IsVariadic() && i == m.Type.NumIn()-1 {
			break
		}
		args = append(args, argFor(in, scope, a, &timeIdx))
	}
	out := m.Func.Call(args)
	var vals []any
	for _, o := range out {
		if e, ok := o.Interface().(error); ok && e != nil {
			err = e
			continue
		}
		if o.Type() == reflect.TypeOf((*error)(nil)).Elem() {
			continue
		}
		vals = append(vals, o.Interface())
	}
	b, _ := json.Marshal(vals)
	return string(b), err
}

// ADR 0002 Decision 2: every repository method, called with household B's scope and arguments
// aimed at household A's rows, can neither read, change nor delete them. Every method without a
// scope is classified, and the zero scope is refused everywhere.
func TestHouseholdIsolation_EveryRepositoryMethodIsConfinedToItsScope(t *testing.T) {
	client, cleanup := setupTestDB(t)
	defer cleanup()
	a := householdFor(t, client, "household-a")
	seedHouseholdA(t, client, a)
	b := scopeAs(t, client, "household-b", markerText, "admin") // even B's admin, named like A's rows
	before := snapshotA(t, client.DB, a.ID)
	for table, rows := range before {
		require.NotEmpty(t, rows, "household A has a row in %s", table)
	}

	var walked []string
	for name, repo := range repositories(client.DB) {
		rt := reflect.TypeOf(repo)
		for i := 0; i < rt.NumMethod(); i++ {
			m := rt.Method(i)
			if !takesScope(m) {
				if _, ok := unscopedMethods[m.Name]; !ok {
					t.Errorf("%s.%s takes no identity.Scope and is not classified in unscopedMethods", name, m.Name)
				}
				continue
			}
			walked = append(walked, name+"."+m.Name)

			// Household B, in autocommit as in production; whatever B wrote is removed afterwards,
			// so the next call's results hold nothing echoed from this one's arguments.
			res, err := call(repo, m, b, a)
			if err == nil {
				for _, marker := range []string{a.ID, markerText, fmt.Sprint(markerNumber)} {
					assert.NotContains(t, res, marker, "%s.%s returned household A's data", name, m.Name)
				}
			}
			after := snapshotA(t, client.DB, a.ID)
			for table := range before {
				assert.Equal(t, before[table], after[table], "%s.%s changed or deleted household A's row in %s", name, m.Name, table)
			}
			removeHousehold(t, client.DB, b.HouseholdID())

			// The zero scope is refused, and writes nothing.
			_, zeroErr := call(repo, m, identity.Scope{}, a)
			assert.Error(t, zeroErr, "%s.%s must refuse the zero scope", name, m.Name)
			assert.Equal(t, before, snapshotA(t, client.DB, a.ID), "%s.%s with the zero scope", name, m.Name)
		}
	}
	sort.Strings(walked)
	t.Logf("walked %d scoped repository methods", len(walked))
	assert.GreaterOrEqual(t, len(walked), 90, "sanity: the walk reaches the repositories")

	// Nothing changed A's data outside the transactions either.
	assert.Equal(t, before, snapshotA(t, client.DB, a.ID))
}
