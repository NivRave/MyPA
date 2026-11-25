package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/toolutil"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
)

// A scheduled workflow is a cron automation rule whose action is a prompt (V4-07): at each fire
// the scheduler runs the instruction as a turn for the rule's person. Migration 11 turned every
// row of the old workflows table into one. The workflow tools keep their names, arguments and
// answers; the rows live in automation_rules.

// PromptPrefix marks a rule action that the assistant runs as a turn for the rule's person.
const PromptPrefix = "prompt:"

// PromptOf returns the instruction of a cron rule whose action is a prompt, and whether it is one.
func PromptOf(rule models.AutomationRule) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(rule.TriggerType), "cron") {
		return "", false
	}
	action := strings.TrimSpace(rule.ActionInstruction)
	if len(action) < len(PromptPrefix) || !strings.EqualFold(action[:len(PromptPrefix)], PromptPrefix) {
		return "", false
	}
	return strings.TrimSpace(action[len(PromptPrefix):]), true
}

// workflowView is how the workflow tools show a scheduled workflow: the fields of the workflows
// table it replaced, so the model reads the same answer as before.
type workflowView struct {
	ID             uint      `json:"id"`
	UserID         string    `json:"user_id"`
	Name           string    `json:"name"`
	CronExpression string    `json:"cron_expression"`
	Instruction    string    `json:"instruction"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
}

// ownWorkflow returns the rule as a workflow when it is one and belongs to the caller: a workflow
// is one person's own, as the workflows table was, even though its rule is in their household.
func ownWorkflow(rule *models.AutomationRule, callCtx orchestrator.CallContext) (workflowView, bool) {
	if rule == nil {
		return workflowView{}, false
	}
	instruction, ok := PromptOf(*rule)
	if !ok {
		return workflowView{}, false
	}
	personID := callCtx.Scope.PersonID()
	mine := rule.UserID == callCtx.ActorID() || (personID != "" && rule.PersonID != nil && *rule.PersonID == personID)
	if !mine {
		return workflowView{}, false
	}
	expr, err := ExtractCronSchedule(rule.ConditionJSON)
	if err != nil {
		expr = rule.ConditionJSON
	}
	return workflowView{ID: rule.ID, UserID: rule.UserID, Name: rule.Name, CronExpression: expr,
		Instruction: instruction, IsActive: rule.IsActive, CreatedAt: rule.CreatedAt}, true
}

// notifyWorkflowsChanged asks the scheduler to reload, so a new or deleted workflow takes effect
// now rather than at the next five-minute reload.
func (m *Module) notifyWorkflowsChanged(callCtx orchestrator.CallContext) {
	if callCtx.Extra != nil {
		if fn, ok := callCtx.Extra["workflows_changed"].(func()); ok && fn != nil {
			fn()
		}
	}
	m.mu.RLock()
	cb := m.workflowsChanged
	m.mu.RUnlock()
	if cb != nil {
		cb()
	}
}

func (m *Module) executeScheduleWorkflow(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	name := strings.TrimSpace(toolutil.String(args, "name"))
	cronExp := strings.TrimSpace(toolutil.String(args, "cron_expression"))
	instruction := strings.TrimSpace(toolutil.String(args, "instruction"))
	if name == "" || cronExp == "" || instruction == "" {
		return "Missing required parameters (name, cron_expression, instruction)", nil
	}
	if _, err := ExtractCronSchedule(cronExp); err != nil {
		return fmt.Sprintf("Error scheduling workflow: %v", err), nil
	}
	condition, err := json.Marshal(map[string]string{"cron": cronExp})
	if err != nil {
		return "", err
	}

	actorID, s := getUserAndScope(callCtx)
	rule := &models.AutomationRule{
		UserID:            actorID,
		HouseholdID:       s.HouseholdID(),
		FamilyGroup:       s.LegacyLabel(),
		Name:              name,
		TriggerType:       "cron",
		TriggerTopic:      "*",
		ConditionJSON:     string(condition),
		ActionInstruction: PromptPrefix + " " + instruction,
		IsActive:          true,
		CreatedAt:         time.Now(),
	}
	if err := repo.CreateAutomationRule(s, rule); err != nil {
		return fmt.Sprintf("Error scheduling workflow: %v", err), nil
	}
	m.notifyWorkflowsChanged(callCtx)

	if bus := m.resolveEventBus(callCtx); bus != nil {
		afterBytes, _ := json.Marshal(rule)
		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "automation",
			Action:      "create",
			EntityType:  "AutomationRule",
			EntityID:    fmt.Sprint(rule.ID),
			ActorUserID: actorID,
			HouseholdID: s.HouseholdID(),
			FamilyGroup: s.LegacyLabel(),
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Scheduled workflow '%s' on cron %s", name, cronExp),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("✅ **Workflow Scheduled!**\n\n'%s' will run on schedule: %s", name, cronExp), nil
}

func (m *Module) executeListWorkflows(callCtx orchestrator.CallContext, repo db.AutomationRepository) (string, error) {
	rules, err := repo.ListAutomationRules(callCtx.Scope, "")
	if err != nil {
		return fmt.Sprintf("Error listing workflows: %v", err), nil
	}
	var workflows []workflowView
	for i := range rules {
		if wf, ok := ownWorkflow(&rules[i], callCtx); ok && wf.IsActive {
			workflows = append(workflows, wf)
		}
	}
	if len(workflows) == 0 {
		return "You have no active workflows scheduled.", nil
	}
	workflowsJSON, _ := json.Marshal(workflows)
	return fmt.Sprintf("Here are your active workflows:\n%s", string(workflowsJSON)), nil
}

func (m *Module) executeDeleteWorkflow(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	idVal := toolutil.String(args, "id")
	if idVal == "" {
		if idInt := toolutil.Int(args, "id", 0); idInt > 0 {
			idVal = fmt.Sprintf("%d", idInt)
		}
	}
	if idVal == "" {
		return "Missing id parameter", nil
	}
	var id uint
	if _, err := fmt.Sscanf(idVal, "%d", &id); err != nil {
		return "Invalid id parameter: must be a number", nil
	}

	actorID, s := getUserAndScope(callCtx)
	rule, err := repo.GetAutomationRule(s, id)
	if err != nil {
		return fmt.Sprintf("Error deleting workflow: %v", err), nil
	}
	if _, ok := ownWorkflow(rule, callCtx); !ok {
		return fmt.Sprintf("Error deleting workflow: you have no workflow with ID %d.", id), nil
	}
	beforeBytes, _ := json.Marshal(models.SnapshotMap(rule)) // the snapshot /undo restores, every column included (BL-68)
	if err := repo.DeleteAutomationRule(s, id); err != nil {
		return fmt.Sprintf("Error deleting workflow: %v", err), nil
	}
	m.notifyWorkflowsChanged(callCtx)

	if bus := m.resolveEventBus(callCtx); bus != nil {
		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "automation",
			Action:      "delete",
			EntityType:  "AutomationRule",
			EntityID:    fmt.Sprintf("%d", id),
			ActorUserID: actorID,
			HouseholdID: s.HouseholdID(),
			FamilyGroup: s.LegacyLabel(),
			BeforeJSON:  string(beforeBytes),
			Summary:     fmt.Sprintf("Deleted workflow #%d", id),
			Timestamp:   time.Now(),
		})
	}

	return "Workflow deleted successfully.", nil
}

// The kinds of rule action ExecuteAction and the scheduler carry out (BL-63). Anything else does
// nothing when the rule fires; /doctor counts such rules (db.Client.IdleRuleCount, which matches
// these tests).

// ErrActionDoesNothing is returned for a rule triggered by an event whose action is an instruction:
// only a scheduled rule carries one out (BL-63).
var ErrActionDoesNothing = errors.New("a rule triggered by an event can call a webhook ('https://...') or send a message ('notify: ...'); " +
	"an instruction for the assistant to carry out runs only on a schedule (trigger_type 'cron')")

// NormalizeAction is the action a new rule is stored with (BL-63): a webhook or a notify: message
// as given; on a cron rule anything else is an instruction the assistant carries out as a turn,
// stored as "prompt: <instruction>". On a rule triggered by an event an instruction would do
// nothing, so it is refused with ErrActionDoesNothing. The chat tool and the GraphQL mutation both
// create rules through it; existing rules are not rewritten.
func NormalizeAction(triggerType, action string) (string, error) {
	action = strings.TrimSpace(action)
	switch {
	case isWebhookAction(action) || isNotifyAction(action):
		return action, nil
	case strings.EqualFold(strings.TrimSpace(triggerType), "cron"):
		if isPromptAction(action) {
			return action, nil
		}
		return PromptPrefix + " " + action, nil
	default:
		return "", ErrActionDoesNothing
	}
}

// isWebhookAction reports a webhook: a URL, or "webhook: <url>".
func isWebhookAction(action string) bool {
	a := strings.TrimSpace(action)
	return strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") || hasPrefixFold(a, "webhook:")
}

// isNotifyAction reports a message to the rule's person: "notify: <text>".
func isNotifyAction(action string) bool { return hasPrefixFold(strings.TrimSpace(action), "notify:") }

// isPromptAction reports an instruction run as a turn: "prompt: <instruction>" (a cron rule only).
func isPromptAction(action string) bool {
	return hasPrefixFold(strings.TrimSpace(action), PromptPrefix)
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
