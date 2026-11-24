package automation

import (
	"encoding/json"
	"strings"

	"github.com/nivik/mypa/internal/models"
)

// NotifiesPerson reports whether the rule's action reaches its person: a message ("notify: ...")
// or, for a cron rule, a turn whose answer goes to them (a scheduled workflow, "prompt: ...",
// V4-07). That is what quiet hours hold back (V4-05). A webhook or any other action reaches no one
// and runs on time.
func NotifiesPerson(rule models.AutomationRule) bool {
	if _, ok := PromptOf(rule); ok {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(rule.ActionInstruction)), "notify:")
}

// CronRuleUrgent reports whether a cron rule's condition marks it urgent
// (`{"cron": "0 23 * * *", "urgent": true}`): its message then goes out even during its person's
// quiet hours. A plain cron string, or a condition without the key, is not urgent.
func CronRuleUrgent(conditionJSON string) bool {
	trimmed := strings.TrimSpace(conditionJSON)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var obj struct {
		Urgent bool `json:"urgent"`
	}
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return false
	}
	return obj.Urgent
}
