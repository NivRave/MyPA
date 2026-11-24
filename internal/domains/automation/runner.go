package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	// concurrency managed by repository
	"time"

	"github.com/nivik/mypa/internal/crypto"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/netguard"
	"github.com/nivik/mypa/internal/orchestrator"
	"github.com/nivik/mypa/internal/packs"
	"github.com/nivik/mypa/internal/proactive"
	"github.com/robfig/cron/v3"
)

var (
	// Blocked CIDR ranges for SSRF defense-in-depth:
	// 127.0.0.0/8 (loopback)
	// 10.0.0.0/8 (private class A)
	// 172.16.0.0/12 (private class B)
	// 192.168.0.0/16 (private class C)
	// 169.254.0.0/16 (link-local, cloud metadata service 169.254.169.254)
	// 0.0.0.0/8 (current network)
	// 100.64.0.0/10 (carrier grade NAT)
	// 198.18.0.0/15 (benchmarking)
	// ::1/128 (IPv6 loopback)
	// fc00::/7 (IPv6 unique local)
	// fe80::/10 (IPv6 link-local)
	blockedCIDRs = []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"0.0.0.0/8",
		"100.64.0.0/10",
		"198.18.0.0/15",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}

	blockedIPNets []*net.IPNet

	blockedHostnames = []string{
		"localhost",
		"127.0.0.1",
		"::1",
		"host.docker.internal",
		"docker.for.mac.localhost",
		"docker.for.win.localhost",
		"gateway.docker.internal",
	}

	blockedSuffixes = []string{
		".localhost",
		".local",
		".internal",
	}
)

func init() {
	for _, cidr := range blockedCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			blockedIPNets = append(blockedIPNets, ipNet)
			_ = blockedHostnames
			_ = blockedSuffixes
		}
	}
}

// IsBlockedIP tests whether a given IP address belongs to loopback, private, link-local,
// cloud metadata, or reserved ranges.
func IsBlockedIP(ip net.IP) bool {
	return netguard.IsBlockedIP(ip)
}

// IsBlockedHostname tests whether a hostname is prohibited (e.g. docker internals, localhost, .internal).
func IsBlockedHostname(host string) bool {
	return netguard.IsBlockedHostname(host)
}

// DecryptAESGCM decrypts ciphertext using AES-256-GCM (delegating to crypto.Decrypt).
func DecryptAESGCM(ciphertext []byte, passphrase ...string) ([]byte, error) {
	pw := ""
	if len(passphrase) > 0 {
		pw = passphrase[0]
	}
	return crypto.Decrypt(ciphertext, pw)
}

// EncryptAESGCM encrypts plaintext using AES-256-GCM (delegating to crypto.Encrypt).
func EncryptAESGCM(plaintext []byte, passphrase ...string) ([]byte, error) {
	pw := ""
	if len(passphrase) > 0 {
		pw = passphrase[0]
	}
	return crypto.Encrypt(plaintext, pw)
}

// ActionExecutor defines a function signature for custom action execution on rule match.
type ActionExecutor func(ctx context.Context, rule models.AutomationRule, event events.MutationEvent) error

// HTTPCallResult represents the output of an external HTTP request.
type HTTPCallResult struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}

// Runner listens for MutationEvents on the EventBus, evaluates active AutomationRules,
// and invokes actions or safe SSRF-protected HTTP endpoints.
type Runner struct {
	repo                    db.AutomationRepository
	bus                     *events.EventBus
	httpClient              *http.Client
	notificationSender      orchestrator.NotificationSender
	actionExecutor          ActionExecutor
	allowLoopbackForTesting bool
	// householdPacks reads a household's packs; when set, a rule runs only while its household
	// has the Automations pack on (V4-27).
	householdPacks func(ctx context.Context, householdID string) (packs.Set, error)
	// stateless runner fields
}

// RunnerOption configures a Runner instance.
type RunnerOption func(*Runner)

// WithNotificationSender sets the NotificationSender callback on the runner.
func WithNotificationSender(sender orchestrator.NotificationSender) RunnerOption {
	return func(r *Runner) {
		r.notificationSender = sender
	}
}

// WithActionExecutor overrides the default action executor on the runner.
func WithActionExecutor(executor ActionExecutor) RunnerOption {
	return func(r *Runner) {
		r.actionExecutor = executor
	}
}

// WithHouseholdPacks makes the runner check, before every action, that the rule's household has
// the Automations pack switched on (V4-27). A failed lookup counts as off: a rule never runs on a
// guess.
func WithHouseholdPacks(lookup func(ctx context.Context, householdID string) (packs.Set, error)) RunnerOption {
	return func(r *Runner) {
		r.householdPacks = lookup
	}
}

// WithAllowLoopbackForTesting allows 127.0.0.1 / localhost exclusively for test harness servers.
func WithAllowLoopbackForTesting(allow bool) RunnerOption {
	return func(r *Runner) {
		r.allowLoopbackForTesting = allow
	}
}

// WithCustomHTTPClient overrides the internal http.Client (e.g. for mock transports in tests).
func WithCustomHTTPClient(client *http.Client) RunnerOption {
	return func(r *Runner) {
		r.httpClient = client
	}
}

// NewRunner creates and initializes a new Automation Runner.
func NewRunner(repo db.AutomationRepository, bus *events.EventBus, opts ...RunnerOption) *Runner {
	r := &Runner{
		repo: repo,
		bus:  bus,
	}

	for _, opt := range opts {
		opt(r)
	}

	if r.httpClient == nil {
		r.httpClient = r.createSSRFProtectedClient()
	}

	if bus != nil {
		bus.SubscribeAsync(r.HandleMutationEvent)
	}

	return r
}

// createSSRFProtectedClient builds an http.Client with connection-level SSRF safeguards.
func (r *Runner) createSSRFProtectedClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}

			if r.allowLoopbackForTesting && (host == "127.0.0.1" || host == "localhost" || host == "::1") {
				return dialer.DialContext(ctx, network, addr)
			}

			if IsBlockedHostname(host) {
				return nil, fmt.Errorf("SSRF blocked: host %s is not permitted", host)
			}

			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("SSRF DNS resolution failed for %s: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("SSRF blocked: no IP addresses found for host %s", host)
			}

			for _, ip := range ips {
				if IsBlockedIP(ip) {
					return nil, fmt.Errorf("SSRF blocked: host %s resolved to prohibited IP %s", host, ip.String())
				}
			}

			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
		ResponseHeaderTimeout: 15 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			if !r.allowLoopbackForTesting {
				if err := r.ValidateURL(req.URL.String()); err != nil {
					return fmt.Errorf("redirect blocked by SSRF protection: %w", err)
				}
			}
			return nil
		},
	}
}

// ValidateURL performs static and DNS-based SSRF checks on a target URL string.
func (r *Runner) ValidateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q: only http and https are allowed", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if strings.TrimSpace(hostname) == "" {
		return errors.New("URL missing hostname")
	}

	if r.allowLoopbackForTesting && (hostname == "127.0.0.1" || hostname == "localhost" || hostname == "::1") {
		return nil
	}

	if IsBlockedHostname(hostname) {
		return fmt.Errorf("SSRF blocked: hostname %q is prohibited", hostname)
	}

	// If hostname is directly an IP address
	if ip := net.ParseIP(hostname); ip != nil {
		if IsBlockedIP(ip) {
			return fmt.Errorf("SSRF blocked: IP address %s is private or reserved", ip.String())
		}
		return nil
	}

	// Resolve hostname to verify destination IPs
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("SSRF DNS lookup failed for host %q: %w", hostname, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("SSRF blocked: no IP addresses resolved for host %q", hostname)
	}

	for _, ip := range ips {
		if IsBlockedIP(ip) {
			return fmt.Errorf("SSRF blocked: host %q resolves to private or reserved IP %s", hostname, ip.String())
		}
	}

	return nil
}

// CallExternal makes an SSRF-protected HTTP request, decrypting headers if provided.
func (r *Runner) CallExternal(ctx context.Context, method, targetURL string, encryptedHeaders []byte, body string) (*HTTPCallResult, error) {
	if err := r.ValidateURL(targetURL); err != nil {
		return nil, err
	}

	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "" {
		m = "GET"
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(ctx, m, targetURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	// Decrypt and inject headers if available
	if len(encryptedHeaders) > 0 {
		decrypted, err := DecryptAESGCM(encryptedHeaders)
		if err == nil && len(decrypted) > 0 {
			// Try parsing as JSON map
			var headerMap map[string]string
			if jsonErr := json.Unmarshal(decrypted, &headerMap); jsonErr == nil {
				for k, v := range headerMap {
					req.Header.Set(k, v)
				}
			} else {
				// Try line-delimited Header: Value
				lines := strings.Split(string(decrypted), "\n")
				for _, line := range lines {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						req.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
					}
				}
			}
		}
	}

	// If body exists and no Content-Type was set, default to application/json
	if body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("external request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read up to 2MB of response to avoid memory exhaustion
	limitReader := io.LimitReader(resp.Body, 2*1024*1024)
	respBytes, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	respHeaders := make(map[string]string)
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}

	return &HTTPCallResult{
		StatusCode: resp.StatusCode,
		Headers:    respHeaders,
		Body:       string(respBytes),
	}, nil
}

// EvaluateCondition evaluates rule conditions against a MutationEvent.
// Returns true if conditions match, or false otherwise.
func EvaluateCondition(conditionJSON string, ev events.MutationEvent) (bool, error) {
	cStr := strings.TrimSpace(conditionJSON)
	if cStr == "" || cStr == "{}" {
		return true, nil
	}

	var cond map[string]interface{}
	if err := json.Unmarshal([]byte(cStr), &cond); err != nil {
		return false, fmt.Errorf("invalid condition_json: %w", err)
	}

	// Parse AfterJSON into a map if available for property checks
	var afterMap map[string]interface{}
	if strings.TrimSpace(ev.AfterJSON) != "" {
		_ = json.Unmarshal([]byte(ev.AfterJSON), &afterMap)
	}

	for key, val := range cond {
		normKey := strings.ToLower(strings.TrimSpace(key))
		valStr := fmt.Sprint(val)

		switch normKey {
		case "action":
			if !strings.EqualFold(ev.Action, valStr) {
				return false, nil
			}
		case "entity_type":
			if !strings.EqualFold(ev.EntityType, valStr) {
				return false, nil
			}
		case "topic":
			if !strings.EqualFold(ev.Topic, valStr) {
				return false, nil
			}
		case "actor_user_id":
			if ev.ActorUserID != valStr {
				return false, nil
			}
		case "family_group":
			if ev.FamilyGroup != valStr {
				return false, nil
			}
		case "keyword":
			needle := strings.ToLower(valStr)
			inSummary := strings.Contains(strings.ToLower(ev.Summary), needle)
			inAfter := strings.Contains(strings.ToLower(ev.AfterJSON), needle)
			if !inSummary && !inAfter {
				return false, nil
			}
		case "field":
			// Complex field condition with operator and value
			fieldName := valStr
			op, _ := cond["operator"].(string)
			targetVal := cond["value"]
			matched := matchFieldCondition(fieldName, op, targetVal, ev, afterMap)
			if !matched {
				return false, nil
			}
		case "operator", "value":
			// Handled in "field" key above; ignore here
			continue
		case "min_amount", "threshold":
			// Numeric threshold check on 'amount' or 'value' in AfterJSON
			if afterMap == nil {
				return false, nil
			}
			targetNum, err := toFloat64(val)
			if err != nil {
				return false, nil
			}
			amountVal, hasAmount := getNumericField(afterMap, "amount", "value", "price", "cost")
			if !hasAmount || amountVal < targetNum {
				return false, nil
			}
		default:
			// Match directly against AfterJSON key
			if afterMap != nil {
				if actualVal, ok := afterMap[key]; ok {
					if fmt.Sprint(actualVal) != valStr {
						return false, nil
					}
				} else {
					return false, nil
				}
			} else {
				return false, nil
			}
		}
	}

	return true, nil
}

func matchFieldCondition(field, op string, targetVal interface{}, ev events.MutationEvent, afterMap map[string]interface{}) bool {
	var actualVal interface{}

	// Check event fields first
	switch strings.ToLower(field) {
	case "action":
		actualVal = ev.Action
	case "topic":
		actualVal = ev.Topic
	case "entity_type":
		actualVal = ev.EntityType
	case "summary":
		actualVal = ev.Summary
	default:
		if afterMap != nil {
			actualVal = afterMap[field]
		}
	}

	if actualVal == nil {
		return false
	}

	normOp := strings.ToLower(strings.TrimSpace(op))
	if normOp == "" || normOp == "==" || normOp == "eq" {
		return fmt.Sprint(actualVal) == fmt.Sprint(targetVal)
	}
	if normOp == "!=" || normOp == "neq" {
		return fmt.Sprint(actualVal) != fmt.Sprint(targetVal)
	}
	if normOp == "contains" {
		return strings.Contains(strings.ToLower(fmt.Sprint(actualVal)), strings.ToLower(fmt.Sprint(targetVal)))
	}

	// Numeric comparisons
	actualNum, errA := toFloat64(actualVal)
	targetNum, errT := toFloat64(targetVal)
	if errA != nil || errT != nil {
		return false
	}

	switch normOp {
	case ">", "gt":
		return actualNum > targetNum
	case ">=", "gte":
		return actualNum >= targetNum
	case "<", "lt":
		return actualNum < targetNum
	case "<=", "lte":
		return actualNum <= targetNum
	}

	return false
}

func toFloat64(val interface{}) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(v), 64)
	default:
		return 0, fmt.Errorf("unsupported number type: %T", val)
	}
}

func getNumericField(m map[string]interface{}, keys ...string) (float64, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if num, err := toFloat64(v); err == nil {
				return num, true
			}
		}
	}
	return 0, false
}

// HandleMutationEvent processes incoming MutationEvents and executes matching active rules.
func (r *Runner) HandleMutationEvent(ctx context.Context, ev events.MutationEvent) error {
	if r.repo == nil {
		return nil
	}

	// Guard against infinite recursive automation triggers
	if strings.EqualFold(ev.Topic, "automation") &&
		(strings.EqualFold(ev.Action, "rule_executed") || strings.EqualFold(ev.Action, "rule_execution_failed")) {
		return nil
	}

	// V4-30: a rule only ever reacts to events of its own household. An event without a
	// household matches no rule.
	if strings.TrimSpace(ev.HouseholdID) == "" {
		return nil
	}

	// Scans every household's active rules (the event runner); the household check below
	// confines each rule to its own household.
	rules, err := r.repo.ListActiveAutomationRules(ev.Topic)
	if err != nil {
		slog.Error("failed to query active automation rules", "topic", ev.Topic, "error", err)
		return nil
	}

	for _, rule := range rules {
		if !rule.IsActive {
			continue
		}

		// Household scope: the rule's household must be the event's. Within a household a rule
		// reacts to every member's mutations, as it did within a family group before V4-30.
		if rule.HouseholdID == "" || rule.HouseholdID != ev.HouseholdID {
			continue
		}
		// BL-67: a row its person did not share is theirs alone, so another member's rule does not
		// act on it (its webhook would send the whole row). The rule's own person's rows count.
		if ev.Unshared() && !ruleOfActor(rule, ev) {
			continue
		}

		// Topic matching: if TriggerTopic is set and not wildcard, it must match event topic
		if rule.TriggerTopic != "" && rule.TriggerTopic != "*" {
			if !strings.EqualFold(rule.TriggerTopic, ev.Topic) {
				continue
			}
		}

		// Trigger type matching: must be an event-reactive trigger type
		if !strings.EqualFold(rule.TriggerType, "event_mutation") &&
			!strings.EqualFold(rule.TriggerType, "keyword") &&
			!strings.EqualFold(rule.TriggerType, "threshold") {
			continue
		}

		// Evaluate rule conditions
		matched, err := EvaluateCondition(rule.ConditionJSON, ev)
		if err != nil {
			slog.Warn("automation condition evaluation error", "rule_id", rule.ID, "error", err)
			continue
		}
		if !matched {
			continue
		}

		// Execute action
		execErr := r.ExecuteAction(ctx, rule, ev)
		if execErr != nil {
			slog.Error("automation rule execution failed", "rule_id", rule.ID, "rule_name", rule.Name, "error", execErr)
			if r.bus != nil {
				_ = r.bus.Publish(ctx, events.MutationEvent{
					Topic:       "automation",
					Action:      "rule_execution_failed",
					EntityType:  "AutomationRule",
					EntityID:    fmt.Sprint(rule.ID),
					ActorUserID: rule.UserID,
					HouseholdID: rule.HouseholdID,
					FamilyGroup: rule.FamilyGroup,
					Summary:     fmt.Sprintf("Rule %q execution failed: %v", rule.Name, execErr),
					Timestamp:   time.Now(),
				})
			}
		}
	}

	return nil
}

// ruleOfActor reports whether the rule's person made the event. Events name their actor by the
// platform ID (the rule tools, the calendar) or by users.id, which is people.id (finance), so both
// of the rule's keys count: user_id and the person_id the database fills (V4-32). A rule whose
// person is unknown matches only by user_id: when in doubt an unshared row is not acted on.
func ruleOfActor(rule models.AutomationRule, ev events.MutationEvent) bool {
	actor := strings.TrimSpace(ev.ActorUserID)
	if actor == "" {
		return false
	}
	if strings.TrimSpace(rule.UserID) == actor {
		return true
	}
	return rule.PersonID != nil && strings.TrimSpace(*rule.PersonID) == actor
}

// ExecuteAction performs the action defined on the automation rule. Both the event runner and the
// scheduler's cron rules come through here, so a household with the Automations pack off runs no
// rule (V4-27): the action is skipped, not failed, and its data is kept.
func (r *Runner) ExecuteAction(ctx context.Context, rule models.AutomationRule, ev events.MutationEvent) error {
	if !r.automationsOn(ctx, rule.HouseholdID) {
		slog.Info("automation rule skipped: the household has the Automations pack off", "rule_id", rule.ID, "household_id", rule.HouseholdID)
		return nil
	}
	// Custom action executor override if registered
	if r.actionExecutor != nil {
		return r.actionExecutor(ctx, rule, ev)
	}

	action := strings.TrimSpace(rule.ActionInstruction)

	// Webhook execution: action starts with http:// or https:// or "webhook:"
	var webhookURL string
	if strings.HasPrefix(action, "http://") || strings.HasPrefix(action, "https://") {
		webhookURL = action
	} else if strings.HasPrefix(strings.ToLower(action), "webhook:") {
		webhookURL = strings.TrimSpace(action[len("webhook:"):])
	}

	if webhookURL != "" {
		eventJSON, _ := json.Marshal(ev)
		_, err := r.CallExternal(ctx, "POST", webhookURL, nil, string(eventJSON))
		if err != nil {
			return fmt.Errorf("webhook call failed: %w", err)
		}
	} else if strings.HasPrefix(strings.ToLower(action), "notify:") {
		// Proactive notification action
		// It waits out its person's quiet hours (BL-49) unless the rule is marked urgent, as a
		// scheduled one does (V4-05).
		msg := strings.TrimSpace(action[len("notify:"):])
		if r.notificationSender != nil && rule.UserID != "" {
			sendCtx := ctx
			if RuleUrgent(rule.ConditionJSON) {
				sendCtx = proactive.Urgent(ctx)
			}
			_ = r.notificationSender(sendCtx, rule.UserID, msg)
		}
	}

	// Publish rule_executed audit event
	if r.bus != nil {
		_ = r.bus.Publish(ctx, events.MutationEvent{
			Topic:       "automation",
			Action:      "rule_executed",
			EntityType:  "AutomationRule",
			EntityID:    fmt.Sprint(rule.ID),
			ActorUserID: rule.UserID,
			HouseholdID: rule.HouseholdID,
			FamilyGroup: rule.FamilyGroup,
			Summary:     fmt.Sprintf("Automation rule %q executed for event %s.%s", rule.Name, ev.Topic, ev.Action),
			Timestamp:   time.Now(),
		})
	}

	return nil
}

// automationsOn reports whether rules of the household may run. Without a pack lookup (tests that
// do not wire one) every household may.
func (r *Runner) automationsOn(ctx context.Context, householdID string) bool {
	if r.householdPacks == nil {
		return true
	}
	set, err := r.householdPacks(ctx, householdID)
	if err != nil {
		slog.Warn("automation rule skipped: could not read the household's packs", "household_id", householdID, "error", err)
		return false
	}
	return set.Has(packs.Automations)
}

// ExtractCronSchedule extracts the cron expression from condition JSON or plain string.
// Supported formats:
// 1. JSON object: `{"cron": "0 9 * * 1"}` or `{"expression": "0 9 * * 1"}` or `{"schedule": "0 9 * * 1"}`
// 2. Plain standard cron string: "0 9 * * 1"
func ExtractCronSchedule(conditionJSON string) (string, error) {
	trimmed := strings.TrimSpace(conditionJSON)
	if trimmed == "" {
		return "", errors.New("empty condition for cron trigger")
	}

	// Try JSON parsing
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			for _, k := range []string{"cron", "cron_expression", "expression", "schedule"} {
				if v, ok := obj[k]; ok {
					if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
						trimmed = strings.TrimSpace(s)
						break
					}
				}
			}
		}
	}

	// Validate cron expression (standard 5-field or descriptor)
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	_, err := parser.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid cron expression %q: %w", trimmed, err)
	}
	return trimmed, nil
}
