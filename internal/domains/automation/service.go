package automation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/orchestrator"
	"net/url"
	"strconv"
	"strings"
)

// Runner returns the internal Automation Runner instance.
func (m *Module) Runner() *Runner {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runner
}

// SetWorkflowsChanged sets or updates the workflow change callback.
func (m *Module) SetWorkflowsChanged(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workflowsChanged = fn
}

func (m *Module) resolveEventBus(callCtx orchestrator.CallContext) *events.EventBus {
	if m.bus != nil {
		return m.bus
	}
	if callCtx.EventBus != nil {
		return callCtx.EventBus
	}
	if bus, ok := callCtx.Extra["event_bus"].(*events.EventBus); ok {
		return bus
	}
	return nil
}

// getUserAndScope returns the acting user's key and the household scope every repository call is
// confined to (V4-30). The household never comes from tool arguments.
func getUserAndScope(callCtx orchestrator.CallContext) (string, identity.Scope) {
	return callCtx.ActorID(), callCtx.Scope
}

func getStringArg(args map[string]interface{}, key string) string {
	if val, ok := args[key].(string); ok {
		return val
	}
	return ""
}

func getInt64Arg(args map[string]interface{}, key string) (int64, bool) {
	if val, ok := args[key].(float64); ok {
		return int64(val), true
	}
	if val, ok := args[key].(int); ok {
		return int64(val), true
	}
	if val, ok := args[key].(int64); ok {
		return val, true
	}
	if str, ok := args[key].(string); ok {
		if parsed, err := strconv.ParseInt(strings.TrimSpace(str), 10, 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func appendQueryParams(targetURL, queryParams string) string {
	trimmed := strings.TrimSpace(queryParams)
	if trimmed == "" {
		return targetURL
	}

	// Try JSON map
	var qMap map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &qMap); err == nil && len(qMap) > 0 {
		values := url.Values{}
		for k, v := range qMap {
			values.Set(k, fmt.Sprint(v))
		}
		if strings.Contains(targetURL, "?") {
			return targetURL + "&" + values.Encode()
		}
		return targetURL + "?" + values.Encode()
	}

	// Direct query string
	trimmed = strings.TrimPrefix(trimmed, "?")
	if strings.Contains(targetURL, "?") {
		return targetURL + "&" + trimmed
	}
	return targetURL + "?" + trimmed
}

func jsonMarshalNoEscape(v interface{}) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}
