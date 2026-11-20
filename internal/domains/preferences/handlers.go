package preferences

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
)

func (m *Module) executeUpdateUserPreferences(ctx context.Context, callCtx orchestrator.CallContext, repo db.PreferencesRepository, args map[string]interface{}) (string, error) {
	userID, s := getUserAndScope(callCtx)
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
			HouseholdID: s.HouseholdID(),
			FamilyGroup: s.LegacyLabel(),
			Summary:     "Updated user customization preferences and standing instructions",
			Timestamp:   time.Now(),
		})
	}

	data, _ := jsonMarshalNoEscape(pref)
	return fmt.Sprintf("User preferences updated successfully:\n%s", data), nil
}

func (m *Module) executeGetUserPreferences(ctx context.Context, callCtx orchestrator.CallContext, repo db.PreferencesRepository, args map[string]interface{}) (string, error) {
	userID, _ := getUserAndScope(callCtx)
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

func (m *Module) executeUpsertPersonalCRM(ctx context.Context, callCtx orchestrator.CallContext, repo db.PreferencesRepository, args map[string]interface{}) (string, error) {
	personName := strings.TrimSpace(getStringArg(args, "person_name"))
	if personName == "" {
		return "", fmt.Errorf("person_name is required")
	}

	contactID, _ := getInt64Arg(args, "id")
	userID, s := getUserAndScope(callCtx)

	var beforeJSON string
	action := "create"
	if contactID > 0 {
		existing, err := repo.GetCRMContact(s, uint(contactID))
		if err != nil {
			return "", fmt.Errorf("failed to verify CRM contact %d: %w", contactID, err)
		}
		isAuthorized := existing != nil && existing.HouseholdID != "" && existing.HouseholdID == s.HouseholdID()
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
		HouseholdID:        s.HouseholdID(),
		FamilyGroup:        s.LegacyLabel(),
		PersonName:         personName,
		Relationship:       relationship,
		ImportantDatesJSON: datesJSON,
		GiftIdeasAndSizes:  giftIdeas,
		MedicalNotes:       medicalNotes,
		UpdatedAt:          time.Now(),
	}

	if err := repo.UpsertCRMContact(s, contact); err != nil {
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
			HouseholdID: s.HouseholdID(),
			FamilyGroup: s.LegacyLabel(),
			BeforeJSON:  beforeJSON,
			AfterJSON:   afterJSON,
			Summary:     fmt.Sprintf("Saved personal CRM contact %s (%s)", contact.PersonName, contact.Relationship),
			Timestamp:   time.Now(),
		})
	}

	return fmt.Sprintf("Successfully saved CRM contact %q (ID: %d, Relationship: %q).", contact.PersonName, contact.ID, contact.Relationship), nil
}

func (m *Module) executeQueryPersonalCRM(ctx context.Context, callCtx orchestrator.CallContext, repo db.PreferencesRepository, args map[string]interface{}) (string, error) {
	_, s := getUserAndScope(callCtx)
	query := getStringArg(args, "query")
	relationshipFilter := strings.ToLower(strings.TrimSpace(getStringArg(args, "relationship")))

	contacts, err := repo.ListCRMContacts(s, query)
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
