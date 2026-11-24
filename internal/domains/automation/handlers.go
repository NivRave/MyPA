package automation

import (
	"context"
	"fmt"
	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
	"strings"
	"time"
)

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
		case "create_automation_rule", "register_external_api", "schedule_workflow", "delete_workflow":
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
	case "schedule_workflow":
		return m.executeScheduleWorkflow(ctx, callCtx, repo, args)
	case "list_workflows":
		return m.executeListWorkflows(callCtx, repo)
	case "delete_workflow":
		return m.executeDeleteWorkflow(ctx, callCtx, repo, args)
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

	userID, s := getUserAndScope(callCtx)

	conn := &models.ExternalAPIConnector{
		UserID:               userID,
		HouseholdID:          s.HouseholdID(),
		FamilyGroup:          s.LegacyLabel(),
		Name:                 name,
		BaseURL:              baseURL,
		DefaultMethod:        method,
		EncryptedAuthHeaders: encryptedHeaders,
		Description:          desc,
		CreatedAt:            time.Now(),
	}

	if err := repo.CreateAPIConnector(s, conn); err != nil {
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
			HouseholdID: s.HouseholdID(),
			FamilyGroup: s.LegacyLabel(),
			Summary:     fmt.Sprintf("Registered external API connector %q (%s)", conn.Name, conn.BaseURL),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("Successfully registered external API connector %q (ID: %d) with base URL %q. Auth headers encrypted securely.", conn.Name, conn.ID, conn.BaseURL), nil
}

func (m *Module) executeListExternalAPIs(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	_, s := getUserAndScope(callCtx)

	connectors, err := repo.ListAPIConnectors(s)
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

	_, s := getUserAndScope(callCtx)

	var targetURL string
	var encryptedHeaders []byte

	if connectorName != "" || connectorID > 0 {
		var conn *models.ExternalAPIConnector
		var err error
		if connectorID > 0 {
			conn, err = repo.GetAPIConnector(s, uint(connectorID))
		} else {
			conn, err = repo.GetAPIConnectorByName(s, connectorName)
		}
		if err != nil {
			return "", fmt.Errorf("failed to look up connector: %w", err)
		}
		isAuthorized := conn != nil && conn.HouseholdID != "" && conn.HouseholdID == s.HouseholdID()
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

	userID, s := getUserAndScope(callCtx)

	rule := &models.AutomationRule{
		UserID:            userID,
		HouseholdID:       s.HouseholdID(),
		FamilyGroup:       s.LegacyLabel(),
		Name:              name,
		TriggerType:       triggerType,
		TriggerTopic:      triggerTopic,
		ConditionJSON:     conditionJSON,
		ActionInstruction: actionInstruction,
		IsActive:          isActive,
		CreatedAt:         time.Now(),
	}

	if err := repo.CreateAutomationRule(s, rule); err != nil {
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
			HouseholdID: s.HouseholdID(),
			FamilyGroup: s.LegacyLabel(),
			Summary:     fmt.Sprintf("Created automation rule %q for topic %s", rule.Name, rule.TriggerTopic),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("Successfully created automation rule %q (ID: %d) for topic %q.", rule.Name, rule.ID, rule.TriggerTopic), nil
}

func (m *Module) executeListAutomationRules(ctx context.Context, callCtx orchestrator.CallContext, repo db.AutomationRepository, args map[string]interface{}) (string, error) {
	_, s := getUserAndScope(callCtx)
	triggerTopic := getStringArg(args, "trigger_topic")

	rules, err := repo.ListAutomationRules(s, triggerTopic)
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

	userID, s := getUserAndScope(callCtx)
	rule, err := repo.GetAutomationRule(s, uint(ruleID))
	if err != nil {
		return "", fmt.Errorf("failed to lookup automation rule: %w", err)
	}
	// V4-30: the lookup is confined to the caller's household, so a rule of another household (or
	// none at all) comes back nil. Refuse it: never report a deletion that did not happen.
	if rule == nil || rule.HouseholdID == "" || rule.HouseholdID != s.HouseholdID() {
		return "", fmt.Errorf("automation rule %d not found or permission denied", ruleID)
	}

	if err := repo.DeleteAutomationRule(s, uint(ruleID)); err != nil {
		return "", fmt.Errorf("failed to delete automation rule: %w", err)
	}
	if bus := m.resolveEventBus(callCtx); bus != nil {
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "automation",
			Action:      "rule_deleted",
			EntityType:  "AutomationRule",
			EntityID:    fmt.Sprint(ruleID),
			ActorUserID: userID,
			HouseholdID: s.HouseholdID(),
			FamilyGroup: s.LegacyLabel(),
			Summary:     fmt.Sprintf("Deleted automation rule %d", ruleID),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("Successfully deleted automation rule %d.", ruleID), nil
}
