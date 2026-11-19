package workspace_gmail

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

// Option configures the workspace_gmail Module.
type Option func(*Module)

// WithClient sets a static GmailClient.
func WithClient(client orchestrator.GmailClient) Option {
	return func(m *Module) {
		m.client = client
	}
}

// WithClientProvider sets a dynamic GmailClient provider function.
func WithClientProvider(provider func() orchestrator.GmailClient) Option {
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

// Module implements orchestrator.ToolModule for Gmail operations.
type Module struct {
	client         orchestrator.GmailClient
	clientProvider func() orchestrator.GmailClient
	bus            *events.EventBus
}

// NewModule initializes the workspace_gmail module.
func NewModule(opts ...Option) *Module {
	m := &Module{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *Module) Name() string {
	return "workspace_gmail"
}

func (m *Module) SystemPromptSection(ctx context.Context, callCtx orchestrator.CallContext) string {
	return ""
}

func (m *Module) getClient() orchestrator.GmailClient {
	if m.clientProvider != nil {
		return m.clientProvider()
	}
	return m.client
}

func (m *Module) Declarations() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		{
			Name:        "search_emails",
			Description: "Searches the user's Gmail using standard Gmail query syntax. Use this to find recent emails or specific threads (e.g., 'newer_than:30d', 'is:unread', 'from:john').",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"query": {
						Type:        genai.TypeString,
						Description: "The Gmail search query (e.g. 'is:unread', 'newer_than:1m').",
					},
					"max_results": {
						Type:        genai.TypeInteger,
						Description: "The maximum number of emails to fetch (default: 5, max 100).",
					},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "read_email",
			Description: "Reads the contents of a specific email by its ID.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message_id": {
						Type:        genai.TypeString,
						Description: "The ID of the email to read.",
					},
				},
				Required: []string{"message_id"},
			},
		},
		{
			Name:        "draft_email_reply",
			Description: "Drafts a reply to an email. Does not send it.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message_id": {
						Type:        genai.TypeString,
						Description: "The ID of the email to reply to.",
					},
					"reply_text": {
						Type:        genai.TypeString,
						Description: "The body of the reply.",
					},
				},
				Required: []string{"message_id", "reply_text"},
			},
		},
		{
			Name:        "archive_emails",
			Description: "Archives one or more emails by removing them from the INBOX.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message_ids": {
						Type:        genai.TypeArray,
						Description: "The IDs of the emails to archive.",
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
					},
				},
				Required: []string{"message_ids"},
			},
		},
		{
			Name:        "soft_delete_emails",
			Description: "Moves one or more emails to the native Gmail Trash bin (soft delete).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message_ids": {
						Type:        genai.TypeArray,
						Description: "The IDs of the emails to delete.",
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
					},
				},
				Required: []string{"message_ids"},
			},
		},
		{
			Name:        "list_email_labels",
			Description: "Lists all available Gmail labels and their IDs for the user.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
			},
		},
		{
			Name:        "apply_email_labels",
			Description: "Applies a specific Gmail label to one or more emails.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message_ids": {
						Type:        genai.TypeArray,
						Description: "The IDs of the emails.",
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
					},
					"label_id": {
						Type:        genai.TypeString,
						Description: "The ID of the label to apply.",
					},
				},
				Required: []string{"message_ids", "label_id"},
			},
		},
		{
			Name:        "create_email_label",
			Description: "Creates a new Gmail label and returns its ID. Use this when you want to categorize an email and the appropriate label doesn't exist yet.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"label_name": {
						Type:        genai.TypeString,
						Description: "The name of the new label (e.g. 'Receipts', 'Newsletters').",
					},
				},
				Required: []string{"label_name"},
			},
		},
		{
			Name:        "unsubscribe_email",
			Description: "Attempts to unsubscribe from a newsletter or mailing list by parsing the List-Unsubscribe header of a specific email. Call this when the user asks to unsubscribe from an email.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"message_id": {
						Type:        genai.TypeString,
						Description: "The ID of the email to unsubscribe from.",
					},
				},
				Required: []string{"message_id"},
			},
		},
	}
}

func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	client := m.getClient()
	if client == nil {
		return "⚠️ Gmail integration is not configured.", nil
	}

	actorID := callCtx.ActorID()
	bus := m.bus
	if bus == nil {
		bus = callCtx.EventBus
	}

	switch toolName {
	case "search_emails":
		query := toolutil.String(args, "query")
		maxResults := toolutil.Int64(args, "max_results", 5)

		slog.Info("executing search_emails tool", "user", actorID, "query", query, "max_results", maxResults)
		emails, err := client.SearchEmails(ctx, actorID, query, maxResults)
		if err != nil {
			slog.Error("search_emails failed", "error", err)
			return fmt.Sprintf("Error checking emails: %v", err), nil
		}
		if len(emails) == 0 {
			return "You have no unread emails.", nil
		}

		emailsJSON, _ := json.Marshal(emails)
		return fmt.Sprintf("Here are the emails I found:\n%s\nYou can use read_email, archive_emails, soft_delete_emails, draft_email_reply, or apply_email_labels on them.", string(emailsJSON)), nil

	case "read_email":
		messageID := toolutil.String(args, "message_id")
		if messageID == "" {
			return "Missing message_id parameter", nil
		}

		slog.Info("executing read_email tool", "user", actorID, "message_id", messageID)
		body, err := client.ReadEmail(ctx, actorID, messageID)
		if err != nil {
			slog.Error("read_email failed", "error", err)
			return fmt.Sprintf("Error reading email: %v", err), nil
		}

		if secState := orchestrator.GetTurnSecurityState(ctx); secState != nil {
			secState.MarkUntrusted("email:" + messageID)
		}
		wrappedBody := orchestrator.WrapUntrustedContent("email", messageID, body)
		return fmt.Sprintf("Here is the email body for %s:\n%s\nYou can take action or summarize it.", messageID, wrappedBody), nil

	case "draft_email_reply":
		messageID := toolutil.String(args, "message_id")
		replyText := toolutil.String(args, "reply_text")
		if messageID == "" || replyText == "" {
			return "Missing message_id or reply_text parameters", nil
		}

		slog.Info("executing draft_email_reply tool", "user", actorID, "message_id", messageID)
		err := client.DraftReply(ctx, actorID, messageID, replyText)
		if err != nil {
			slog.Error("draft_email_reply failed", "error", err)
			return fmt.Sprintf("Error drafting reply: %v", err), nil
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "communication",
			Action:      "create",
			EntityType:  "EmailDraft",
			EntityID:    messageID,
			ActorUserID: actorID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			Summary:     fmt.Sprintf("Drafted reply to email %s", messageID),
		})

		return "Draft created successfully! The user can review it in their Gmail app.", nil

	case "archive_emails":
		messageIDs := toolutil.StringSlice(args, "message_ids")
		if len(messageIDs) == 0 {
			return "Missing message_ids parameter", nil
		}

		slog.Info("executing archive_emails tool", "user", actorID, "count", len(messageIDs))
		for _, messageID := range messageIDs {
			if err := client.ArchiveEmail(ctx, actorID, messageID); err != nil {
				slog.Error("archive_emails failed", "error", err, "message_id", messageID)
			}
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "communication",
			Action:      "update",
			EntityType:  "EmailMessage",
			EntityID:    fmt.Sprintf("%d_messages", len(messageIDs)),
			ActorUserID: actorID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			Summary:     fmt.Sprintf("Archived %d emails", len(messageIDs)),
		})

		return fmt.Sprintf("%d emails archived successfully.", len(messageIDs)), nil

	case "soft_delete_emails":
		messageIDs := toolutil.StringSlice(args, "message_ids")
		if len(messageIDs) == 0 {
			return "Missing message_ids parameter", nil
		}

		slog.Info("executing soft_delete_emails tool", "user", actorID, "count", len(messageIDs))
		for _, messageID := range messageIDs {
			if err := client.SoftDeleteEmail(ctx, actorID, messageID); err != nil {
				slog.Error("soft_delete_emails failed", "error", err, "message_id", messageID)
			}
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "communication",
			Action:      "delete",
			EntityType:  "EmailMessage",
			EntityID:    fmt.Sprintf("%d_messages", len(messageIDs)),
			ActorUserID: actorID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			Summary:     fmt.Sprintf("Moved %d emails to trash", len(messageIDs)),
		})

		return fmt.Sprintf("%d emails moved to trash successfully.", len(messageIDs)), nil

	case "list_email_labels":
		slog.Info("executing list_email_labels tool", "user", actorID)
		labels, err := client.ListLabels(ctx, actorID)
		if err != nil {
			slog.Error("list_email_labels failed", "error", err)
			return fmt.Sprintf("Error listing labels: %v", err), nil
		}
		labelsJSON, _ := json.Marshal(labels)
		return fmt.Sprintf("Here are the available labels (name -> ID):\n%s", string(labelsJSON)), nil

	case "apply_email_labels":
		messageIDs := toolutil.StringSlice(args, "message_ids")
		labelID := toolutil.String(args, "label_id")
		if len(messageIDs) == 0 || labelID == "" {
			return "Missing message_ids or label_id parameter", nil
		}

		slog.Info("executing apply_email_labels tool", "user", actorID, "count", len(messageIDs), "label_id", labelID)
		for _, messageID := range messageIDs {
			if err := client.ApplyLabel(ctx, actorID, messageID, labelID); err != nil {
				slog.Error("apply_email_labels failed", "error", err, "message_id", messageID)
			}
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "communication",
			Action:      "update",
			EntityType:  "EmailLabel",
			EntityID:    labelID,
			ActorUserID: actorID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			Summary:     fmt.Sprintf("Applied label %s to %d emails", labelID, len(messageIDs)),
		})

		return fmt.Sprintf("Label applied to %d emails successfully.", len(messageIDs)), nil

	case "create_email_label":
		labelName := toolutil.String(args, "label_name")
		if labelName == "" {
			return "❌ Invalid label name.", nil
		}

		slog.Info("executing create_email_label tool", "user", actorID, "label_name", labelName)
		labelID, err := client.CreateLabel(ctx, actorID, labelName)
		if err != nil {
			return fmt.Sprintf("Error creating label: %v", err), nil
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "communication",
			Action:      "create",
			EntityType:  "EmailLabel",
			EntityID:    labelID,
			ActorUserID: actorID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			Summary:     fmt.Sprintf("Created email label %s", labelName),
		})

		return fmt.Sprintf("Label '%s' created successfully with ID: %s", labelName, labelID), nil

	case "unsubscribe_email":
		messageID := toolutil.String(args, "message_id")
		if messageID == "" {
			return "Missing message_id parameter", nil
		}

		slog.Info("executing unsubscribe_email tool", "user", actorID, "message_id", messageID)
		err := client.UnsubscribeEmail(ctx, actorID, messageID)
		if err != nil {
			return fmt.Sprintf("Error unsubscribing: %v", err), nil
		}

		_ = toolutil.PublishMutation(ctx, bus, events.MutationEvent{
			Topic:       "communication",
			Action:      "update",
			EntityType:  "EmailSubscription",
			EntityID:    messageID,
			ActorUserID: actorID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			Summary:     fmt.Sprintf("Unsubscribed from email %s", messageID),
		})

		return "Successfully unsubscribed from the email list.", nil

	default:
		return "", fmt.Errorf("unknown tool %s in workspace_gmail", toolName)
	}
}
