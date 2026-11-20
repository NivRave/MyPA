package identity

import "strings"

// Role is a person's role in their household (ADR 0002 Decision 1, V4-35). The operator is not a
// role: it is a flag on the person (Scope.operator), for the install rather than a household.
type Role string

const (
	// RoleOwner manages the household: members, invitations, roles, settings, and each member's
	// activity (not another adult's private chats or personal data).
	RoleOwner Role = "owner"
	// RoleAdult uses the assistant and the dashboard: their own data and the household's shared
	// data.
	RoleAdult Role = "adult"
	// RoleChild chats only: no dashboard.
	RoleChild Role = "child"
	// RoleGuest chats only, and only in the topics shared with them by name.
	RoleGuest Role = "guest"
)

// Roles lists every role, for the test that walks every role and action pair.
var Roles = []Role{RoleOwner, RoleAdult, RoleChild, RoleGuest}

// ParseRole maps a stored role to a Role: the four role names, and the two names used before
// V4-35 as Migration C maps them ("admin" is an owner, "family" an adult). It is trimmed and
// ignores case. Anything else is no role at all, which may do nothing: an unknown string never
// gains powers.
func ParseRole(s string) Role {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(RoleOwner), "admin":
		return RoleOwner
	case string(RoleAdult), "family":
		return RoleAdult
	case string(RoleChild):
		return RoleChild
	case string(RoleGuest):
		return RoleGuest
	}
	return ""
}

// ParseInviteRole is the role a pairing code (and later an invite) may give: adult, child or
// guest, never owner. An owner is made by promoting a member (ADR 0002 Decision 5). The legacy
// names map as ParseRole maps them, except that "admin" and "owner" give an adult. ok is false
// for a string that names no role.
func ParseInviteRole(s string) (Role, bool) {
	switch r := ParseRole(s); r {
	case "":
		return "", false
	case RoleOwner:
		return RoleAdult, true
	default:
		return r, true
	}
}

// LegacyRole is what users.role is written as beside the membership's role, until V4-44 drops
// it: "admin" for an owner, "family" for everyone else, the two values the release before V4
// reads (ADR 0002 Decision 2: writes go to both through V4.0).
func LegacyRole(r Role) string {
	if r == RoleOwner {
		return "admin"
	}
	return "family"
}

// Action is something a scope may or may not do.
type Action string

const (
	// UseAssistant is chatting and running tools.
	UseAssistant Action = "use_assistant"
	// SignInDashboard is asking for a dashboard sign-in link (/dashboard).
	SignInDashboard Action = "sign_in_dashboard"
	// UseDashboard is reading and changing, in the dashboard, the household's shared data and
	// one's own personal data.
	UseDashboard Action = "use_dashboard"
	// ManageHousehold is changing the household's settings.
	ManageHousehold Action = "manage_household"
	// ManageMembers is listing the members and their roles, and issuing pairing codes.
	ManageMembers Action = "manage_members"
	// ViewMemberActivity is seeing each member's activity: who, when and what kind of action,
	// without the text of their chats or their personal data.
	ViewMemberActivity Action = "view_member_activity"
	// RunBackup is the /backup command (operator).
	RunBackup Action = "run_backup"
	// RestoreBackup is the /restore command (operator).
	RestoreBackup Action = "restore_backup"
	// RunDoctor is the /doctor command (operator).
	RunDoctor Action = "run_doctor"
)

// Actions lists every action, for the test that walks every role and action pair.
var Actions = []Action{UseAssistant, SignInDashboard, UseDashboard, ManageHousehold, ManageMembers,
	ViewMemberActivity, RunBackup, RestoreBackup, RunDoctor}

// roleActions is the one role-to-action table (ADR 0002 Decision 1). What a child or a guest may
// do tool by tool is V4-11's.
var roleActions = map[Role]map[Action]bool{
	RoleOwner: {UseAssistant: true, SignInDashboard: true, UseDashboard: true, ManageHousehold: true,
		ManageMembers: true, ViewMemberActivity: true},
	RoleAdult: {UseAssistant: true, SignInDashboard: true, UseDashboard: true},
	RoleChild: {UseAssistant: true},
	RoleGuest: {UseAssistant: true},
}

// operatorActions are what the operator flag allows whatever the household role: the install's
// commands, and asking for a sign-in link (ADR 0002 Decision 5: owner, adult and operator). The
// flag gives no access to a household's content (question 2).
var operatorActions = map[Action]bool{RunBackup: true, RestoreBackup: true, RunDoctor: true, SignInDashboard: true}

func allowed(r Role, operator bool, a Action) bool {
	return roleActions[r][a] || (operator && operatorActions[a])
}
