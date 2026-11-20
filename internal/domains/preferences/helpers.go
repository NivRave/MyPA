package preferences

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/nivik/mypa/internal/identity"
	"github.com/nivik/mypa/internal/orchestrator"
)

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

// normalizeJSONSlice accepts a JSON array or a comma-separated list and returns a JSON array.
func normalizeJSONSlice(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "[]"
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		return trimmed
	}
	var list []string
	for _, it := range strings.Split(trimmed, ",") {
		if s := strings.TrimSpace(it); s != "" {
			list = append(list, s)
		}
	}
	b, _ := json.Marshal(list)
	return string(b)
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
