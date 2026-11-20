package workspace_contacts

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nivik/mypa/internal/domains/toolutil"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/orchestrator"
	"google.golang.org/genai"
)

// Option configures the workspace_contacts Module.
type Option func(*Module)

// WithClient sets a static ContactsClient.
func WithClient(client orchestrator.ContactsClient) Option {
	return func(m *Module) {
		m.client = client
	}
}

// WithClientProvider sets a dynamic ContactsClient provider function.
func WithClientProvider(provider func() orchestrator.ContactsClient) Option {
	return func(m *Module) {
		m.clientProvider = provider
	}
}

// WithEventBus sets the event bus.
func WithEventBus(bus *events.EventBus) Option {
	return func(m *Module) {
		m.bus = bus
	}
}

// Module implements orchestrator.ToolModule for Google Contacts operations.
type Module struct {
	client         orchestrator.ContactsClient
	clientProvider func() orchestrator.ContactsClient
	bus            *events.EventBus
}

// NewModule initializes the workspace_contacts module.
func NewModule(opts ...Option) *Module {
	m := &Module{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *Module) Name() string {
	return "workspace_contacts"
}

func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	return ""
}

func (m *Module) getClient() orchestrator.ContactsClient {
	if m.clientProvider != nil {
		return m.clientProvider()
	}
	return m.client
}

func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "search_contacts",
			Description: "Searches the user's Google Contacts by name. Call this when the user asks for someone's email or phone number.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"query": {
						Type:        genai.TypeString,
						Description: "The name or part of the name of the contact to search for.",
					},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "create_contact",
			Description: "Creates a new Google Contact. Call this when the user asks to save a phone number, email, or contact information.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"name": {
						Type:        genai.TypeString,
						Description: "The name of the person.",
					},
					"email": {
						Type:        genai.TypeString,
						Description: "The email address of the person (optional).",
					},
					"phone": {
						Type:        genai.TypeString,
						Description: "The phone number of the person (optional).",
					},
				},
				Required: []string{"name"},
			},
		},
	}
}

func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	client := m.getClient()
	if client == nil {
		return "⚠️ Google Contacts integration is not configured.", nil
	}

	actorID := callCtx.ActorID()
	bus := m.bus
	if bus == nil {
		bus = callCtx.EventBus
	}

	switch toolName {
	case "search_contacts":
		query := toolutil.String(args, "query")
		if query == "" {
			return "Missing query parameter", nil
		}

		slog.Info("executing search_contacts tool", "user", actorID, "query", query)
		contacts, err := client.SearchContacts(ctx, actorID, query)
		if err != nil {
			slog.Error("search_contacts failed", "error", err)
			return fmt.Sprintf("Error searching contacts: %v", err), nil
		}
		if len(contacts) == 0 {
			return "I couldn't find any contacts matching that name.", nil
		}

		contactsJSON, _ := json.Marshal(contacts)
		return fmt.Sprintf("Here are the contacts I found:\n%s\nPlease provide the requested information to the user.", string(contactsJSON)), nil

	case "create_contact":
		name := toolutil.String(args, "name")
		if name == "" {
			return "Missing name parameter", nil
		}
		email := toolutil.String(args, "email")
		phone := toolutil.String(args, "phone")

		slog.Info("executing create_contact tool", "user", actorID, "name", name)
		err := client.CreateContact(ctx, actorID, name, email, phone)
		if err != nil {
			slog.Error("create_contact failed", "error", err)
			return fmt.Sprintf("Error creating contact: %v", err), nil
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "contacts",
			Action:      "create",
			EntityType:  "Contact",
			EntityID:    name,
			ActorUserID: actorID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			Summary:     fmt.Sprintf("Created contact '%s'", name),
		})

		return "Contact created successfully.", nil

	default:
		return "", fmt.Errorf("unknown tool %s in workspace_contacts", toolName)
	}
}
