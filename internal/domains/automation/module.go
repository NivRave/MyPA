package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/toolutil"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
)

// Option configures an automation Module instance.
type Option func(*Module)

// WithRepository sets the database repository on the module.
func WithRepository(repo db.AutomationRepository) Option {
	return func(m *Module) {
		m.repo = repo
	}
}

// WithEventBus sets the event bus on the module.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) {
		m.bus = bus
	}
}

// WithRunner sets a custom runner on the module.
func WithRunner(r *Runner) Option {
	return func(m *Module) {
		m.runner = r
	}
}

// WithAllowLoopback sets loopback testing permission on the runner.
func WithAllowLoopback(allow bool) Option {
	return func(m *Module) {
		m.allowLoopback = allow
	}
}

// WithWorkflowsChanged sets the workflow change callback.
func WithWorkflowsChanged(fn func()) Option {
	return func(m *Module) {
		m.workflowsChanged = fn
	}
}

// Module implements orchestrator.ToolModule for on-demand external APIs,
// event-driven automation rules, user customization preferences, and personal CRM.
type Module struct {
	repo             db.AutomationRepository
	runner           *Runner
	bus              *events.EventBus
	workflowsChanged func()
	allowLoopback    bool
	mu               sync.RWMutex
}

// NewModule initializes a new automation Module.
func NewModule(repo db.AutomationRepository, opts ...Option) *Module {
	m := &Module{
		repo: repo,
	}
	for _, opt := range opts {
		opt(m)
	}

	if m.runner == nil {
		var runnerOpts []RunnerOption
		if m.allowLoopback {
			runnerOpts = append(runnerOpts, WithAllowLoopbackForTesting(true))
		}
		m.runner = NewRunner(repo, m.bus, runnerOpts...)
	}

	return m
}

// Name returns the unique tool module name.
func (m *Module) Name() string {
	return "automation"
}

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

// Declarations returns the Gemini FunctionDeclarations provided by this module.
func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "register_external_api",
			Description: "Register an external REST API or Webhook connector with securely encrypted authentication headers (e.g. Bearer token, API Key).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"name": {
						Type:        genai.TypeString,
						Description: "Unique identifier name for this API connector (e.g. 'weather_api', 'smart_home_webhook', 'municipal_alerts').",
					},
					"base_url": {
						Type:        genai.TypeString,
						Description: "Base URL of the external API endpoint (e.g. 'https://api.weather.example.com/v1'). Must be valid HTTP or HTTPS.",
					},
					"default_method": {
						Type:        genai.TypeString,
						Description: "Default HTTP method (GET, POST, PUT, DELETE). Defaults to GET if not specified.",
					},
					"auth_headers": {
						Type:        genai.TypeString,
						Description: "Authentication headers (e.g. JSON map '{\"Authorization\": \"Bearer <token>\"}' or 'X-API-Key: secret'). These are encrypted at rest with AES-256-GCM.",
					},
					"description": {
						Type:        genai.TypeString,
						Description: "Optional explanation of what this API provides and how it should be used.",
					},
				},
				Required: []string{"name", "base_url"},
			},
		},
		{
			Name:        "list_external_apis",
			Description: "List all registered external API connectors available to the user and family group (with auth headers securely masked).",
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: map[string]*genai.Schema{},
			},
		},
		{
			Name:        "call_external_api",
			Description: "Execute an external HTTP request using a registered API connector or direct URL, protected by strict SSRF guardrails against local and private networks.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"connector_name": {
						Type:        genai.TypeString,
						Description: "Name of the registered API connector to call.",
					},
					"connector_id": {
						Type:        genai.TypeInteger,
						Description: "Optional numeric ID of the registered API connector.",
					},
					"url": {
						Type:        genai.TypeString,
						Description: "Optional direct HTTP/HTTPS URL (if not using a registered connector). Subject to SSRF validation.",
					},
					"path": {
						Type:        genai.TypeString,
						Description: "Sub-path to append to the connector's base URL (e.g. '/current-conditions' or '/devices/toggle').",
					},
					"method": {
						Type:        genai.TypeString,
						Description: "HTTP method (GET, POST, PUT, DELETE). Defaults to the connector's default or GET.",
					},
					"query_params": {
						Type:        genai.TypeString,
						Description: "Optional JSON key-value map or query string of URL parameters (e.g. '{\"city\": \"Tel Aviv\", \"units\": \"metric\"}').",
					},
					"body": {
						Type:        genai.TypeString,
						Description: "Optional request payload body (typically JSON string).",
					},
				},
			},
		},
		{
			Name:        "create_automation_rule",
			Description: "Create an event-driven automation rule (When -> If -> Then) triggered by data mutations, keywords, or threshold limits.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"name": {
						Type:        genai.TypeString,
						Description: "Descriptive name for the automation rule (e.g. 'Notify spouse when baby fever logged', 'Send webhook on large expense').",
					},
					"trigger_type": {
						Type:        genai.TypeString,
						Description: "Trigger category: 'event_mutation' (data change), 'keyword', 'threshold', or 'cron'.",
					},
					"trigger_topic": {
						Type:        genai.TypeString,
						Description: "Life domain or topic to monitor: 'baby', 'finance', 'house', 'schedule', 'memory', or '*' for all.",
					},
					"condition_json": {
						Type:        genai.TypeString,
						Description: "JSON condition rule (e.g. '{\"action\": \"create\"}', '{\"keyword\": \"fever\"}', or '{\"min_amount\": 500}').",
					},
					"action_instruction": {
						Type:        genai.TypeString,
						Description: "Action to take when triggered: webhook URL (e.g. 'https://api.example.com/webhook'), notification instruction ('notify: Check baby temperature'), or task instruction.",
					},
					"is_active": {
						Type:        genai.TypeBoolean,
						Description: "Whether the rule is immediately active (defaults to true).",
					},
				},
				Required: []string{"name", "trigger_type", "trigger_topic", "action_instruction"},
			},
		},
		{
			Name:        "list_automation_rules",
			Description: "List configured automation rules, optionally filtered by topic.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"trigger_topic": {
						Type:        genai.TypeString,
						Description: "Optional topic filter (e.g. 'baby', 'finance', 'schedule').",
					},
				},
			},
		},
		{
			Name:        "delete_automation_rule",
			Description: "Delete an existing automation rule by its numeric ID.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"rule_id": {
						Type:        genai.TypeInteger,
						Description: "The ID of the rule to delete.",
					},
				},
				Required: []string{"rule_id"},
			},
		},
		{
			Name:        "update_user_preferences",
			Description: "Update runtime user preferences, persona tone, default currency, quiet hours, briefing topics, and custom standing instructions for the assistant.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"preferred_language": {
						Type:        genai.TypeString,
						Description: "User's preferred language for replies (e.g. 'English', 'Hebrew', 'Spanish').",
					},
					"tone_persona": {
						Type:        genai.TypeString,
						Description: "Assistant persona tone (e.g. 'Concise & Direct', 'Warm & Encouraging', 'Humorous', 'Analytical').",
					},
					"default_currency": {
						Type:        genai.TypeString,
						Description: "Default currency code (e.g. 'ILS', 'USD', 'EUR').",
					},
					"quiet_hours_start": {
						Type:        genai.TypeString,
						Description: "Start of quiet hours in 24h format (e.g. '22:00').",
					},
					"quiet_hours_end": {
						Type:        genai.TypeString,
						Description: "End of quiet hours in 24h format (e.g. '07:30').",
					},
					"briefing_topics": {
						Type:        genai.TypeString,
						Description: "JSON array of topics to include in morning briefings (e.g. '[\"schedule\", \"baby\", \"finance\"]').",
					},
					"standing_instructions": {
						Type:        genai.TypeString,
						Description: "Permanent standing instructions from the user dynamically injected into every LLM prompt turn.",
					},
				},
			},
		},
		{
			Name:        "get_user_preferences",
			Description: "Retrieve current user customization preferences, persona settings, and standing instructions.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
			},
		},
		{
			Name:        "upsert_personal_crm",
			Description: "Save or update a personal/family CRM contact with important dates (birthdays, anniversaries), gift ideas, clothing/shoe sizes, and medical notes.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"id": {
						Type:        genai.TypeInteger,
						Description: "Optional contact ID if updating an existing contact.",
					},
					"person_name": {
						Type:        genai.TypeString,
						Description: "Full or familiar name of the person (e.g. 'Grandma Sarah', 'Alice', 'Dr. Cohen').",
					},
					"relationship": {
						Type:        genai.TypeString,
						Description: "Relationship to user/family (e.g. 'Mother', 'Sister', 'Spouse', 'Pediatrician', 'Friend').",
					},
					"important_dates": {
						Type:        genai.TypeString,
						Description: "JSON array of important dates (e.g. '[{\"label\": \"Birthday\", \"date\": \"1988-11-12\"}, {\"label\": \"Anniversary\", \"date\": \"2018-06-20\"}]').",
					},
					"gift_ideas_and_sizes": {
						Type:        genai.TypeString,
						Description: "Gift preferences, clothing sizes, shoe sizes, hobbies, or wish list items.",
					},
					"medical_notes": {
						Type:        genai.TypeString,
						Description: "Important medical notes, allergies, prescriptions, or dietary restrictions.",
					},
				},
				Required: []string{"person_name"},
			},
		},
		{
			Name:        "query_personal_crm",
			Description: "Search personal and family CRM contacts by name, relationship, gift ideas, or medical notes.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"query": {
						Type:        genai.TypeString,
						Description: "Search term to match across names, relationships, gift notes, or medical notes.",
					},
					"relationship": {
						Type:        genai.TypeString,
						Description: "Optional relationship filter.",
					},
				},
			},
		},
		{
			Name:        "schedule_workflow",
			Description: "Schedules a recurring autonomous prompt/workflow instruction using a cron expression.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"name": {
						Type:        genai.TypeString,
						Description: "A descriptive name for the workflow.",
					},
					"cron_expression": {
						Type:        genai.TypeString,
						Description: "Standard 5-part cron expression (e.g. '0 9 * * *' for daily at 9am, '0 8 * * 1' for every Monday at 8am).",
					},
					"instruction": {
						Type:        genai.TypeString,
						Description: "The prompt instruction or autonomous action the assistant will execute on schedule.",
					},
				},
				Required: []string{"name", "cron_expression", "instruction"},
			},
		},
		{
			Name:        "list_workflows",
			Description: "Lists all scheduled recurring workflows and their cron expressions for the current user.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
			},
		},
		{
			Name:        "delete_workflow",
			Description: "Deletes a scheduled recurring workflow by its ID.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"id": {
						Type:        genai.TypeString,
						Description: "The ID of the workflow to delete.",
					},
				},
				Required: []string{"id"},
			},
		},
	}
}

// Execute dispatches and runs tools provided by the automation module.
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	hasUntrusted := callCtx.HasUntrustedContent
	untrustedSources := callCtx.UntrustedSources
	if !hasUntrusted {
		if secState := orchestrator.GetTurnSecurityState(ctx); secState != nil {
			isUntrusted, sources := secState.IsUntrusted()
			if isUntrusted {
				hasUntrusted = true
				untrustedSources = sources
			}
		}
	}

	if hasUntrusted {
		switch toolName {
		case "update_user_preferences", "create_automation_rule", "register_external_api", "schedule_workflow", "delete_workflow":
			sourceDesc := "external untrusted content"
			if len(untrustedSources) > 0 {
				sourceDesc = strings.Join(untrustedSources, ", ")
			}
			return fmt.Sprintf(
				"🔒 **Interactive Security Confirmation Required**\n\n"+
					"The assistant received a command to perform a privileged system action (`%s`) while processing external data (%s).\n"+
					"To prevent indirect prompt injection, this modification was NOT applied automatically.\n\n"+
					"👉 If you intended to make this change, please reply with:\n`CONFIRM %s`",
				toolName, sourceDesc, strings.ToUpper(toolName),
			), nil
		}
	}

	switch toolName {
	case "schedule_workflow":
		return m.executeScheduleWorkflow(ctx, callCtx, args)
	case "list_workflows":
		return m.executeListWorkflows(ctx, callCtx, args)
	case "delete_workflow":
		return m.executeDeleteWorkflow(ctx, callCtx, args)
	}

	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return "", err
	}

	switch toolName {
	case "register_external_api":
		return m.executeRegisterExternalAPI(ctx, callCtx, repo, args)
	case "list_external_apis":
		return m.executeListExternalAPIs(ctx, callCtx, repo, args)
	case "call_external_api":
		return m.executeCallExternalAPI(ctx, callCtx, repo, args)
	case "create_automation_rule":
		return m.executeCreateAutomationRule(ctx, callCtx, repo, args)
	case "list_automation_rules":
		return m.executeListAutomationRules(ctx, callCtx, repo, args)
	case "delete_automation_rule":
		return m.executeDeleteAutomationRule(ctx, callCtx, repo, args)
	case "update_user_preferences":
		return m.executeUpdateUserPreferences(ctx, callCtx, repo, args)
	case "get_user_preferences":
		return m.executeGetUserPreferences(ctx, callCtx, repo, args)
	case "upsert_personal_crm":
		return m.executeUpsertPersonalCRM(ctx, callCtx, repo, args)
	case "query_personal_crm":
		return m.executeQueryPersonalCRM(ctx, callCtx, repo, args)
	default:
		return "", fmt.Errorf("automation: unknown tool %q", toolName)
	}
}

func (m *Module) executeRegisterExternalAPI(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	name := strings.TrimSpace(getStringArg(args, "name"))
	if name == "" {
		return "", fmt.Errorf("name is required for external API registration")
	}

	baseURL := strings.TrimSpace(getStringArg(args, "base_url"))
	if baseURL == "" {
		return "", fmt.Errorf("base_url is required for external API registration")
	}

	method := strings.ToUpper(strings.TrimSpace(getStringArg(args, "default_method")))
	if method == "" {
		method = "GET"
	}

	desc := strings.TrimSpace(getStringArg(args, "description"))
	authHeaders := getStringArg(args, "auth_headers")

	var encryptedHeaders []byte
	if authHeaders != "" {
		enc, err := EncryptAESGCM([]byte(authHeaders))
		if err != nil {
			return "", fmt.Errorf("failed to encrypt auth headers: %w", err)
		}
		encryptedHeaders = enc
	}

	userID, familyGroup := getUserAndFamily(callCtx)

	conn := &models.ExternalAPIConnector{
		UserID:               userID,
		FamilyGroup:          familyGroup,
		Name:                 name,
		BaseURL:              baseURL,
		DefaultMethod:        method,
		EncryptedAuthHeaders: encryptedHeaders,
		Description:          desc,
		CreatedAt:            time.Now(),
	}

	if err := repo.CreateAPIConnector(conn); err != nil {
		return "", fmt.Errorf("failed to save API connector: %w", err)
	}

	// Publish MutationEvent
	if bus := m.resolveEventBus(callCtx); bus != nil {
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "automation",
			Action:      "register_connector",
			EntityType:  "ExternalAPIConnector",
			EntityID:    fmt.Sprint(conn.ID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			Summary:     fmt.Sprintf("Registered external API connector %q (%s)", conn.Name, conn.BaseURL),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("Successfully registered external API connector %q (ID: %d) with base URL %q. Auth headers encrypted securely.", conn.Name, conn.ID, conn.BaseURL), nil
}

func (m *Module) executeListExternalAPIs(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	userID, familyGroup := getUserAndFamily(callCtx)

	connectors, err := repo.ListAPIConnectors(userID, familyGroup)
	if err != nil {
		return "", fmt.Errorf("failed to list connectors: %w", err)
	}

	if len(connectors) == 0 {
		return "No external API connectors registered.", nil
	}

	type safeConnectorView struct {
		ID            uint      `json:"id"`
		Name          string    `json:"name"`
		BaseURL       string    `json:"base_url"`
		DefaultMethod string    `json:"default_method"`
		HasAuth       bool      `json:"has_auth"`
		Description   string    `json:"description"`
		CreatedAt     time.Time `json:"created_at"`
	}

	var views []safeConnectorView
	for _, c := range connectors {
		views = append(views, safeConnectorView{
			ID:            c.ID,
			Name:          c.Name,
			BaseURL:       c.BaseURL,
			DefaultMethod: c.DefaultMethod,
			HasAuth:       len(c.EncryptedAuthHeaders) > 0,
			Description:   c.Description,
			CreatedAt:     c.CreatedAt,
		})
	}

	data, _ := jsonMarshalNoEscape(views)
	return data, nil
}

func (m *Module) executeCallExternalAPI(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	connectorName := strings.TrimSpace(getStringArg(args, "connector_name"))
	connectorID, _ := getInt64Arg(args, "connector_id")
	rawURL := strings.TrimSpace(getStringArg(args, "url"))
	path := strings.TrimSpace(getStringArg(args, "path"))
	method := strings.ToUpper(strings.TrimSpace(getStringArg(args, "method")))
	queryParams := getStringArg(args, "query_params")
	body := getStringArg(args, "body")

	userID, familyGroup := getUserAndFamily(callCtx)

	var targetURL string
	var encryptedHeaders []byte

	if connectorName != "" || connectorID > 0 {
		var conn *models.ExternalAPIConnector
		var err error
		if connectorID > 0 {
			conn, err = repo.GetAPIConnector(uint(connectorID))
		} else {
			conn, err = repo.GetAPIConnectorByName(userID, familyGroup, connectorName)
		}
		if err != nil {
			return "", fmt.Errorf("failed to look up connector: %w", err)
		}
		isAuthorized := conn != nil && ((familyGroup != "" && conn.FamilyGroup == familyGroup) || (conn.UserID != "" && conn.UserID == userID))
		if !isAuthorized {
			return "", fmt.Errorf("API connector not found or permission denied")
		}

		targetURL = strings.TrimRight(conn.BaseURL, "/")
		if path != "" {
			targetURL = targetURL + "/" + strings.TrimLeft(path, "/")
		}
		encryptedHeaders = conn.EncryptedAuthHeaders
		if method == "" {
			method = conn.DefaultMethod
		}
	} else if rawURL != "" {
		targetURL = rawURL
	} else {
		return "", fmt.Errorf("either connector_name, connector_id, or url is required")
	}

	if method == "" {
		method = "GET"
	}

	// Append query parameters if provided
	if queryParams != "" {
		targetURL = appendQueryParams(targetURL, queryParams)
	}

	runner := m.Runner()
	if runner == nil {
		runner = NewRunner(repo, m.resolveEventBus(callCtx))
	}

	res, err := runner.CallExternal(ctx, method, targetURL, encryptedHeaders, body)
	if err != nil {
		return "", fmt.Errorf("external call failed: %w", err)
	}

	bodyPreview := res.Body
	if len(bodyPreview) > 4096 {
		bodyPreview = bodyPreview[:4096] + "\n...[truncated for length]"
	}

	return fmt.Sprintf("HTTP %d OK\nResponse Body:\n%s", res.StatusCode, bodyPreview), nil
}

func (m *Module) executeCreateAutomationRule(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	name := strings.TrimSpace(getStringArg(args, "name"))
	if name == "" {
		return "", fmt.Errorf("rule name is required")
	}

	triggerType := strings.ToLower(strings.TrimSpace(getStringArg(args, "trigger_type")))
	if triggerType == "" {
		triggerType = "event_mutation"
	}

	triggerTopic := strings.ToLower(strings.TrimSpace(getStringArg(args, "trigger_topic")))
	if triggerTopic == "" {
		triggerTopic = "*"
	}

	actionInstruction := strings.TrimSpace(getStringArg(args, "action_instruction"))
	if actionInstruction == "" {
		return "", fmt.Errorf("action_instruction is required")
	}

	conditionJSON := strings.TrimSpace(getStringArg(args, "condition_json"))
	if conditionJSON == "" {
		conditionJSON = "{}"
	}

	isActive := true
	if activeVal, ok := args["is_active"].(bool); ok {
		isActive = activeVal
	}

	userID, familyGroup := getUserAndFamily(callCtx)

	rule := &models.AutomationRule{
		UserID:            userID,
		FamilyGroup:       familyGroup,
		Name:              name,
		TriggerType:       triggerType,
		TriggerTopic:      triggerTopic,
		ConditionJSON:     conditionJSON,
		ActionInstruction: actionInstruction,
		IsActive:          isActive,
		CreatedAt:         time.Now(),
	}

	if err := repo.CreateAutomationRule(rule); err != nil {
		return "", fmt.Errorf("failed to create automation rule: %w", err)
	}

	// Publish MutationEvent
	if bus := m.resolveEventBus(callCtx); bus != nil {
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "automation",
			Action:      "rule_created",
			EntityType:  "AutomationRule",
			EntityID:    fmt.Sprint(rule.ID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			Summary:     fmt.Sprintf("Created automation rule %q for topic %s", rule.Name, rule.TriggerTopic),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("Successfully created automation rule %q (ID: %d) for topic %q.", rule.Name, rule.ID, rule.TriggerTopic), nil
}

func (m *Module) executeListAutomationRules(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	userID, familyGroup := getUserAndFamily(callCtx)
	triggerTopic := getStringArg(args, "trigger_topic")

	rules, err := repo.ListAutomationRules(userID, familyGroup, triggerTopic)
	if err != nil {
		return "", fmt.Errorf("failed to list automation rules: %w", err)
	}

	if len(rules) == 0 {
		return "No automation rules found.", nil
	}

	data, _ := jsonMarshalNoEscape(rules)
	return data, nil
}

func (m *Module) executeDeleteAutomationRule(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	ruleID, ok := getInt64Arg(args, "rule_id")
	if !ok || ruleID <= 0 {
		return "", fmt.Errorf("valid rule_id is required")
	}

	userID, familyGroup := getUserAndFamily(callCtx)
	rule, err := repo.GetAutomationRule(uint(ruleID))
	if err != nil {
		return "", fmt.Errorf("failed to lookup automation rule: %w", err)
	}
	if rule != nil {
		isAuthorized := (familyGroup != "" && rule.FamilyGroup == familyGroup) || (rule.UserID != "" && rule.UserID == userID)
		if !isAuthorized {
			return "", fmt.Errorf("automation rule %d not found or permission denied", ruleID)
		}
	}

	if err := repo.DeleteAutomationRule(uint(ruleID)); err != nil {
		return "", fmt.Errorf("failed to delete automation rule: %w", err)
	}
	if bus := m.resolveEventBus(callCtx); bus != nil {
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "automation",
			Action:      "rule_deleted",
			EntityType:  "AutomationRule",
			EntityID:    fmt.Sprint(ruleID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			Summary:     fmt.Sprintf("Deleted automation rule %d", ruleID),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("Successfully deleted automation rule %d.", ruleID), nil
}

func (m *Module) executeUpdateUserPreferences(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	userID, familyGroup := getUserAndFamily(callCtx)
	if userID == "" {
		return "", fmt.Errorf("unable to identify user from call context")
	}

	pref, err := repo.GetUserPreference(userID)
	if err != nil {
		return "", fmt.Errorf("failed to load user preferences: %w", err)
	}
	if pref == nil {
		pref = &models.UserPreference{
			UserID:             userID,
			DefaultCurrency:    "USD",
			PreferredLanguage:  "English",
			BriefingTopicsJSON: "[]",
		}
	}

	if lang := strings.TrimSpace(getStringArg(args, "preferred_language")); lang != "" {
		pref.PreferredLanguage = lang
	}
	if tone := strings.TrimSpace(getStringArg(args, "tone_persona")); tone != "" {
		pref.TonePersona = tone
	}
	if curr := strings.TrimSpace(getStringArg(args, "default_currency")); curr != "" {
		pref.DefaultCurrency = strings.ToUpper(curr)
	}
	if qStart := strings.TrimSpace(getStringArg(args, "quiet_hours_start")); qStart != "" {
		pref.QuietHoursStart = qStart
	}
	if qEnd := strings.TrimSpace(getStringArg(args, "quiet_hours_end")); qEnd != "" {
		pref.QuietHoursEnd = qEnd
	}
	if inst := strings.TrimSpace(getStringArg(args, "standing_instructions")); inst != "" {
		pref.StandingInstructions = inst
	}
	if topics := getStringArg(args, "briefing_topics"); topics != "" {
		pref.BriefingTopicsJSON = normalizeJSONSlice(topics)
	}

	pref.UpdatedAt = time.Now()
	if err := repo.UpsertUserPreference(pref); err != nil {
		return "", fmt.Errorf("failed to save user preferences: %w", err)
	}

	if bus := m.resolveEventBus(callCtx); bus != nil {
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "automation",
			Action:      "preferences_updated",
			EntityType:  "UserPreference",
			EntityID:    userID,
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			Summary:     "Updated user customization preferences and standing instructions",
			Timestamp:   time.Now(),
		})
	}

	data, _ := jsonMarshalNoEscape(pref)
	return fmt.Sprintf("User preferences updated successfully:\n%s", data), nil
}

func (m *Module) executeGetUserPreferences(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	userID, _ := getUserAndFamily(callCtx)
	if userID == "" {
		return "", fmt.Errorf("unable to determine user ID")
	}

	pref, err := repo.GetUserPreference(userID)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve user preference: %w", err)
	}
	if pref == nil {
		return fmt.Sprintf("No customized preferences found for user %q. Default values apply.", userID), nil
	}

	data, _ := jsonMarshalNoEscape(pref)
	return data, nil
}

func (m *Module) executeUpsertPersonalCRM(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	personName := strings.TrimSpace(getStringArg(args, "person_name"))
	if personName == "" {
		return "", fmt.Errorf("person_name is required")
	}

	contactID, _ := getInt64Arg(args, "id")
	userID, familyGroup := getUserAndFamily(callCtx)

	var beforeJSON string
	action := "create"
	if contactID > 0 {
		existing, err := repo.GetCRMContact(uint(contactID))
		if err != nil {
			return "", fmt.Errorf("failed to verify CRM contact %d: %w", contactID, err)
		}
		isAuthorized := existing != nil && ((familyGroup != "" && existing.FamilyGroup == familyGroup) || (existing.UserID != "" && existing.UserID == userID))
		if !isAuthorized {
			return "", fmt.Errorf("CRM contact %d not found or permission denied", contactID)
		}
		b, _ := json.Marshal(existing)
		beforeJSON = string(b)
		action = "update"
	}

	relationship := strings.TrimSpace(getStringArg(args, "relationship"))
	giftIdeas := strings.TrimSpace(getStringArg(args, "gift_ideas_and_sizes"))
	medicalNotes := strings.TrimSpace(getStringArg(args, "medical_notes"))
	datesJSON := normalizeJSONSlice(getStringArg(args, "important_dates"))

	contact := &models.PersonalCRMContact{
		ID:                 uint(contactID),
		UserID:             userID,
		FamilyGroup:        familyGroup,
		PersonName:         personName,
		Relationship:       relationship,
		ImportantDatesJSON: datesJSON,
		GiftIdeasAndSizes:  giftIdeas,
		MedicalNotes:       medicalNotes,
		UpdatedAt:          time.Now(),
	}

	if err := repo.UpsertCRMContact(contact); err != nil {
		return "", fmt.Errorf("failed to save CRM contact: %w", err)
	}

	var afterJSON string
	if b, err := json.Marshal(contact); err == nil {
		afterJSON = string(b)
	}

	if bus := m.resolveEventBus(callCtx); bus != nil {
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "automation",
			Action:      action,
			EntityType:  "PersonalCRMContact",
			EntityID:    fmt.Sprint(contact.ID),
			ActorUserID: userID,
			FamilyGroup: familyGroup,
			BeforeJSON:  beforeJSON,
			AfterJSON:   afterJSON,
			Summary:     fmt.Sprintf("Saved personal CRM contact %s (%s)", contact.PersonName, contact.Relationship),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("Successfully saved CRM contact %q (ID: %d, Relationship: %q).", contact.PersonName, contact.ID, contact.Relationship), nil
}

func (m *Module) executeQueryPersonalCRM(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	userID, familyGroup := getUserAndFamily(callCtx)
	query := getStringArg(args, "query")
	relationshipFilter := strings.ToLower(strings.TrimSpace(getStringArg(args, "relationship")))

	contacts, err := repo.ListCRMContacts(userID, familyGroup, query)
	if err != nil {
		return "", fmt.Errorf("failed to query CRM contacts: %w", err)
	}

	if relationshipFilter != "" {
		var filtered []models.PersonalCRMContact
		for _, c := range contacts {
			if strings.Contains(strings.ToLower(c.Relationship), relationshipFilter) {
				filtered = append(filtered, c)
			}
		}
		contacts = filtered
	}

	if len(contacts) == 0 {
		return "No matching CRM contacts found.", nil
	}

	data, _ := jsonMarshalNoEscape(contacts)
	return data, nil
}

// SystemPromptSection dynamically injects user preferences, persona tone,
// standing instructions, and personal CRM alerts (upcoming birthdays/anniversaries) into the LLM system prompt.
func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	var sb strings.Builder
	sb.WriteString("### Runtime Customization, Preferences & Personal CRM:\n")
	sb.WriteString("• You manage on-demand external APIs (`register_external_api`, `call_external_api`), event-driven automations (`create_automation_rule`), user preferences (`update_user_preferences`), and family/personal CRM (`upsert_personal_crm`, `query_personal_crm`).\n")

	userID, familyGroup := getUserAndFamily(callCtx)
	repo, err := m.resolveRepo(callCtx)
	if err == nil && repo != nil && userID != "" {
		pref, _ := repo.GetUserPreference(userID)
		if pref != nil {
			if pref.TonePersona != "" {
				sb.WriteString(fmt.Sprintf("• Tone/Persona: %s\n", pref.TonePersona))
			}
			if pref.DefaultCurrency != "" {
				sb.WriteString(fmt.Sprintf("• Default Currency: %s\n", pref.DefaultCurrency))
			}
			if pref.PreferredLanguage != "" {
				sb.WriteString(fmt.Sprintf("• Preferred Language: %s\n", pref.PreferredLanguage))
			}
			if pref.BriefingTopicsJSON != "" && pref.BriefingTopicsJSON != "[]" {
				sb.WriteString(fmt.Sprintf("• Active Briefing Topics: %s\n", pref.BriefingTopicsJSON))
			}
			if pref.QuietHoursStart != "" && pref.QuietHoursEnd != "" {
				sb.WriteString(fmt.Sprintf("• Quiet Hours: %s to %s (suppress non-urgent alerts)\n", pref.QuietHoursStart, pref.QuietHoursEnd))
			}
			if pref.StandingInstructions != "" {
				sb.WriteString(fmt.Sprintf("• User Standing Instructions (STRICT REQUIREMENT):\n  %s\n", pref.StandingInstructions))
			}
		}

		// Check Personal CRM for upcoming birthdays / anniversaries within 30 days
		now := time.Now()
		contacts, _ := repo.ListCRMContacts(userID, familyGroup, "")
		var alerts []string

		for _, contact := range contacts {
			if contact.ImportantDatesJSON == "" || contact.ImportantDatesJSON == "[]" {
				continue
			}

			type dateItem struct {
				Label string `json:"label"`
				Date  string `json:"date"`
			}
			var dates []dateItem
			if err := json.Unmarshal([]byte(contact.ImportantDatesJSON), &dates); err == nil {
				for _, d := range dates {
					daysAway, ok := calculateDaysUntil(d.Date, now)
					if ok && daysAway >= 0 && daysAway <= 30 {
						timeLabel := fmt.Sprintf("in %d days", daysAway)
						if daysAway == 0 {
							timeLabel = "TODAY!"
						} else if daysAway == 1 {
							timeLabel = "tomorrow!"
						}

						extra := ""
						if contact.GiftIdeasAndSizes != "" {
							extra = fmt.Sprintf(" (Gift ideas/sizes: %s)", contact.GiftIdeasAndSizes)
						}
						alerts = append(alerts, fmt.Sprintf("%s (%s) - %s on %s (%s)%s",
							contact.PersonName, contact.Relationship, d.Label, d.Date, timeLabel, extra))
					}
				}
			}
		}

		if len(alerts) > 0 {
			sb.WriteString("\n• Upcoming Important Dates & Personal CRM Alerts (Next 30 Days):\n")
			for _, alert := range alerts {
				sb.WriteString(fmt.Sprintf("  - %s\n", alert))
			}
		}
	}

	return sb.String()
}

func calculateDaysUntil(dateStr string, now time.Time) (int, bool) {
	dStr := strings.TrimSpace(dateStr)
	var month time.Month
	var day int

	// Format: YYYY-MM-DD
	if t, err := time.Parse("2006-01-02", dStr); err == nil {
		month, day = t.Month(), t.Day()
	} else if t, err := time.Parse("01-02", dStr); err == nil {
		// Format: MM-DD
		month, day = t.Month(), t.Day()
	} else if t, err := time.Parse("2006/01/02", dStr); err == nil {
		month, day = t.Month(), t.Day()
	} else {
		return 0, false
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	targetThisYear := time.Date(now.Year(), month, day, 0, 0, 0, 0, now.Location())

	if targetThisYear.Before(today) {
		targetNextYear := time.Date(now.Year()+1, month, day, 0, 0, 0, 0, now.Location())
		days := int(targetNextYear.Sub(today).Hours() / 24)
		return days, true
	}

	days := int(targetThisYear.Sub(today).Hours() / 24)
	return days, true
}

func (m *Module) resolveRepo(callCtx orchestrator.CallContext) (db.AutomationRepository, error) {
	if m.repo != nil {
		return m.repo, nil
	}
	if repo, ok := callCtx.DB.(db.AutomationRepository); ok {
		return repo, nil
	}
	if extraRepo, ok := callCtx.Extra["automation_repo"].(db.AutomationRepository); ok {
		return extraRepo, nil
	}
	return nil, fmt.Errorf("automation: no automation repository available in module or call context")
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

func getUserAndFamily(callCtx orchestrator.CallContext) (string, string) {
	return callCtx.ActorID(), callCtx.TenantFamilyGroup()
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

func normalizeJSONSlice(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "[]"
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		return trimmed
	}
	// Comma-separated fallback
	items := strings.Split(trimmed, ",")
	var list []string
	for _, it := range items {
		if s := strings.TrimSpace(it); s != "" {
			list = append(list, s)
		}
	}
	bytes, _ := json.Marshal(list)
	return string(bytes)
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

func (m *Module) executeScheduleWorkflow(ctx context.Context, callCtx orchestrator.CallContext, args map[string]interface{}) (string, error) {
	name := toolutil.String(args, "name")
	cronExp := toolutil.String(args, "cron_expression")
	instruction := toolutil.String(args, "instruction")

	if name == "" || cronExp == "" || instruction == "" {
		return "Missing required parameters (name, cron_expression, instruction)", nil
	}

	actorID := callCtx.ActorID()
	familyGroup := callCtx.TenantFamilyGroup()

	if callCtx.DB == nil {
		return "❌ Database client not configured.", nil
	}

	wf := models.Workflow{
		UserID:         actorID,
		Name:           name,
		CronExpression: cronExp,
		Instruction:    instruction,
		IsActive:       true,
		CreatedAt:      time.Now(),
	}

	if err := callCtx.DB.SaveWorkflow(wf); err != nil {
		return fmt.Sprintf("Error scheduling workflow: %v", err), nil
	}

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

	if bus := m.resolveEventBus(callCtx); bus != nil {
		afterBytes, _ := json.Marshal(wf)
		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "automation",
			Action:      "create",
			EntityType:  "Workflow",
			EntityID:    fmt.Sprintf("%s:%s", actorID, name),
			ActorUserID: actorID,
			FamilyGroup: familyGroup,
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Scheduled workflow '%s' on cron %s", name, cronExp),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("✅ **Workflow Scheduled!**\n\n'%s' will run on schedule: %s", name, cronExp), nil
}

func (m *Module) executeListWorkflows(ctx context.Context, callCtx orchestrator.CallContext, args map[string]interface{}) (string, error) {
	actorID := callCtx.ActorID()
	if callCtx.DB == nil {
		return "❌ Database client not configured.", nil
	}

	workflows, err := callCtx.DB.GetUserWorkflows(actorID)
	if err != nil {
		return fmt.Sprintf("Error listing workflows: %v", err), nil
	}
	if len(workflows) == 0 {
		return "You have no active workflows scheduled.", nil
	}

	workflowsJSON, _ := json.Marshal(workflows)
	return fmt.Sprintf("Here are your active workflows:\n%s", string(workflowsJSON)), nil
}

func (m *Module) executeDeleteWorkflow(ctx context.Context, callCtx orchestrator.CallContext, args map[string]interface{}) (string, error) {
	idVal := toolutil.String(args, "id")
	if idVal == "" {
		idInt := toolutil.Int(args, "id", 0)
		if idInt > 0 {
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

	actorID := callCtx.ActorID()
	familyGroup := callCtx.TenantFamilyGroup()

	if callCtx.DB == nil {
		return "❌ Database client not configured.", nil
	}

	if err := callCtx.DB.DeleteWorkflow(id, actorID); err != nil {
		return fmt.Sprintf("Error deleting workflow: %v", err), nil
	}

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

	if bus := m.resolveEventBus(callCtx); bus != nil {
		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "automation",
			Action:      "delete",
			EntityType:  "Workflow",
			EntityID:    fmt.Sprintf("%d", id),
			ActorUserID: actorID,
			FamilyGroup: familyGroup,
			Summary:     fmt.Sprintf("Deleted workflow #%d", id),
			Timestamp:   time.Now(),
		})
	}

	return "Workflow deleted successfully.", nil
}

