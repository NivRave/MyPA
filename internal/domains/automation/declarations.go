package automation

import (
	"google.golang.org/genai"
)

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
						Description: "Descriptive name for the automation rule (e.g. 'Notify spouse when a health note is saved', 'Send webhook on large expense').",
					},
					"trigger_type": {
						Type:        genai.TypeString,
						Description: "Trigger category: 'event_mutation' (data change), 'keyword', 'threshold', or 'cron'.",
					},
					"trigger_topic": {
						Type:        genai.TypeString,
						Description: "Life domain or topic to monitor: one of the household's topics (e.g. 'health', 'family'), 'schedule', 'memory', or '*' for all.",
					},
					"condition_json": {
						Type:        genai.TypeString,
						Description: "JSON condition rule (e.g. '{\"action\": \"create\"}', '{\"keyword\": \"fever\"}', or '{\"min_amount\": 500}').",
					},
					"action_instruction": {
						Type:        genai.TypeString,
						Description: "What the rule does when it fires: call a webhook (a URL, e.g. 'https://api.example.com/webhook'), send the person a message ('notify: Check the thermostat'), or, on a schedule only (trigger_type 'cron'), carry out an instruction as a turn for the person, whose answer they receive ('prompt: Summarise this week's spending'). A plain instruction on a cron rule is stored as 'prompt: ...'; a rule triggered by an event cannot carry out an instruction and is refused.",
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
						Description: "Optional topic filter: one of the household's topics (e.g. 'health', 'family') or 'schedule'.",
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
