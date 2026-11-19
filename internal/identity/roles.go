package identity

import "strings"

// Role is a person's role. V4-30 keeps today's two (ADR 0002 §9); V4-35 replaces them with
// owner, adult, child and guest plus the operator flag.
type Role string

const (
	// RoleAdmin may do everything an admin may today, the operator commands included.
	RoleAdmin Role = "admin"
	// RoleFamily is every other member.
	RoleFamily Role = "family"
)

// ParseRole maps a stored role string to a Role. Anything that is not "admin" (trimmed, ignoring
// case) is a family member: an unknown role never gains admin powers.
func ParseRole(s string) Role {
	if strings.EqualFold(strings.TrimSpace(s), string(RoleAdmin)) {
		return RoleAdmin
	}
	return RoleFamily
}

// Action is something a scope may or may not do.
type Action string

const (
	// UseAssistant is chatting, running tools and reading and writing the household's data.
	UseAssistant Action = "use_assistant"
	// ManageHousehold is changing the household's settings.
	ManageHousehold Action = "manage_household"
	// RunBackup is the /backup command (operator).
	RunBackup Action = "run_backup"
	// RestoreBackup is the /restore command (operator).
	RestoreBackup Action = "restore_backup"
	// RunDoctor is the /doctor command (operator).
	RunDoctor Action = "run_doctor"
)

// Actions lists every action, for the test that walks every role and action pair.
var Actions = []Action{UseAssistant, ManageHousehold, RunBackup, RestoreBackup, RunDoctor}

// Roles lists every role.
var Roles = []Role{RoleAdmin, RoleFamily}

// operatorActions need the operator flag; in V4-30 every admin is an operator.
var operatorActions = map[Action]bool{RunBackup: true, RestoreBackup: true, RunDoctor: true}

// roleActions is the one role-to-action table (ADR 0002 Decision 1), with today's two rules.
var roleActions = map[Role]map[Action]bool{
	RoleAdmin:  {UseAssistant: true, ManageHousehold: true},
	RoleFamily: {UseAssistant: true},
}

func allowed(r Role, operator bool, a Action) bool {
	if operatorActions[a] {
		return operator
	}
	return roleActions[r][a]
}
