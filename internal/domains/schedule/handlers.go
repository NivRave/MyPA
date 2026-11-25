package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/domains/toolutil"
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
)

// generalTopic is the topic of a plain calendar event: no [Topic] prefix in Google.
const generalTopic = "general"

// Replies of the Google-only paths (events that exist only in Google), kept from the
// workspace_calendar module these tools replaced (V4-08).
const (
	msgGoogleNotConfigured = "⚠️ Google Calendar integration is not configured."
	msgGoogleNotConnected  = "⚠️ I don't have access to your Google Calendar. Please send /connect to authorize me, then try again."
	msgGoogleExpired       = "⚠️ Your Google connection expired or is invalid. Please send /connect again."
)

// Execute dispatches and runs the calendar domain's tools.
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return "", err
	}

	switch toolName {
	case "create_calendar_event":
		return m.executeCreateEvent(ctx, callCtx, repo, args)
	case "list_calendar_events":
		return m.executeListEvents(ctx, callCtx, repo, args)
	case "configure_topic_calendar":
		return m.executeConfigureTopicCalendar(ctx, callCtx, repo, args)
	case "delete_calendar_event":
		return m.executeDeleteEvent(ctx, callCtx, repo, args)
	case "update_calendar_event":
		return m.executeUpdateEvent(ctx, callCtx, repo, args)
	default:
		return "", fmt.Errorf("schedule: unknown tool %q", toolName)
	}
}

func (m *Module) executeCreateEvent(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	title := strings.TrimSpace(getStringArg(args, "title"))
	if title == "" {
		return "", fmt.Errorf("title is required")
	}

	topic := strings.ToLower(strings.TrimSpace(getStringArg(args, "topic")))
	if topic == "" {
		topic = generalTopic
	}

	loc, err := locationArg(args)
	if err != nil {
		return "", err
	}

	startTimeStr := strings.TrimSpace(getStringArg(args, "start_time"))
	if startTimeStr == "" {
		return "", fmt.Errorf("start_time is required")
	}
	startTime, err := parseFlexibleTimeIn(startTimeStr, loc)
	if err != nil {
		return "", fmt.Errorf("invalid start_time %q: %w", startTimeStr, err)
	}

	endTimeStr := strings.TrimSpace(getStringArg(args, "end_time"))
	var endTime time.Time
	if endTimeStr != "" {
		endTime, err = parseFlexibleTimeIn(endTimeStr, loc)
		if err != nil {
			return "", fmt.Errorf("invalid end_time %q: %w", endTimeStr, err)
		}
	} else {
		endTime = startTime.Add(1 * time.Hour)
	}

	description := strings.TrimSpace(getStringArg(args, "description"))
	tags := getStringSliceArg(args, "tags")
	recurrence := getRecurrenceArg(args)

	userID := callerID(callCtx)
	familyGroup := ""
	if callCtx.User != nil {
		familyGroup = callCtx.User.FamilyGroup
	}

	// Topic configuration lookup
	topicCfg, _ := repo.GetTopicCalendarConfig(userID, topic)

	// Sync to Google default determination
	syncToGoogle := true
	if val, ok := args["sync_to_google"].(bool); ok {
		syncToGoogle = val
	} else if topicCfg != nil && topicCfg.SyncToGoogleDefault != nil {
		syncToGoogle = *topicCfg.SyncToGoogleDefault
	}

	// Shared topic determination
	isShared := false
	if val, ok := args["is_shared"].(bool); ok {
		isShared = val
	} else if familyGroup != "" && models.IsDefaultSharedTopic(topic) {
		isShared = true
	}

	prefixTag := ""
	colorID := ""
	if topicCfg != nil {
		prefixTag = topicCfg.PrefixTag
		colorID = topicCfg.ColorID
	}

	ev := &models.ScheduleEvent{
		UserID:          userID,
		Topic:           topic,
		Title:           title,
		Description:     description,
		StartTime:       startTime,
		EndTime:         endTime,
		RecurrenceRule:  strings.Join(recurrence, "\n"),
		SyncToGoogle:    syncToGoogle,
		IsShared:        isShared,
		CreatedByUserID: userID,
		CreatedAt:       time.Now(),
	}
	_ = ev.SetTags(tags)

	formattedTitle := calendarTitle(ev, prefixTag)
	calEv := googleEvent(formattedTitle, description, startTime, endTime, loc, recurrence)

	// Virtual Sub-Calendar: Google Calendar sync for primary user
	gState := googleOff
	if syncToGoogle {
		var calClient orchestrator.CalendarClient
		calClient, gState = m.googleCalendar(ctx, callCtx, userID, "create")
		if calClient != nil {
			var googleID string
			if coloredCal, ok := calClient.(ColoredCalendarClient); ok && colorID != "" {
				googleID, err = coloredCal.CreateEventWithColor(ctx, calEv, colorID)
			} else {
				googleID, err = calClient.CreateEvent(ctx, calEv)
			}
			switch {
			case err != nil:
				logGoogleFailure("create", userID, err)
				gState = googleFailed
			case googleID != "":
				ev.ExternalGoogleEventID = googleID
			}
		}
	}

	// Partner Calendar Sync (Topic 9.3). A partner without Google is normal; a failing one is reported.
	partnerFailed := false
	if isShared && syncToGoogle && familyGroup != "" {
		partnerUsers := m.findPartnerUsers(ctx, callCtx, userID)
		for _, partner := range partnerUsers {
			partnerCal, pState := m.googleCalendar(ctx, callCtx, partner.PlatformID, "create_partner")
			if pState == googleFailed {
				partnerFailed = true
			}
			if partnerCal != nil {
				var partnerID string
				if coloredCal, ok := partnerCal.(ColoredCalendarClient); ok && colorID != "" {
					partnerID, err = coloredCal.CreateEventWithColor(ctx, calEv, colorID)
				} else {
					partnerID, err = partnerCal.CreateEvent(ctx, calEv)
				}
				if err != nil {
					logGoogleFailure("create_partner", partner.PlatformID, err)
					partnerFailed = true
					continue
				}
				if partnerID != "" {
					ev.PartnerGoogleEventID = partnerID
					break
				}
			}
		}
	}

	// Save to DB
	if err := repo.CreateScheduleEvent(callCtx.Scope, ev); err != nil {
		return "", fmt.Errorf("failed to save schedule event: %w", err)
	}

	// Emit MutationEvent
	bus := m.resolveEventBus(callCtx)
	if bus != nil {
		afterBytes, _ := json.Marshal(ev)
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "schedule",
			Action:      "create",
			EntityType:  "ScheduleEvent",
			EntityID:    strconv.FormatUint(uint64(ev.ID), 10),
			ActorUserID: userID,
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Scheduled '%s' under topic '%s' for %s", formattedTitle, ev.Topic, ev.StartTime.Format(time.RFC822)),
			Timestamp:   time.Now(),
		})
	}

	// Build response message
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📅 **Scheduled Event Created** (ID: %d)\n", ev.ID))
	sb.WriteString(fmt.Sprintf("• **Title**: %s\n", formattedTitle))
	sb.WriteString(fmt.Sprintf("• **Topic**: %s\n", ev.Topic))
	sb.WriteString(fmt.Sprintf("• **Start**: %s\n", ev.StartTime.In(loc).Format(time.RFC1123)))
	sb.WriteString(fmt.Sprintf("• **End**: %s\n", ev.EndTime.In(loc).Format(time.RFC1123)))

	if len(tags) > 0 {
		sb.WriteString(fmt.Sprintf("• **Tags**: %s\n", strings.Join(tags, ", ")))
	}
	if description != "" {
		sb.WriteString(fmt.Sprintf("• **Details**: %s\n", description))
	}

	// Nothing retries a failed sync later, so never promise "pending" (BL-1).
	switch {
	case !ev.SyncToGoogle:
		sb.WriteString("• **Google Calendar**: Internal-only (database)\n")
	case ev.ExternalGoogleEventID != "":
		sb.WriteString("• **Google Calendar**: Synced ✅\n")
	case gState == googleNotConnected:
		sb.WriteString("• **Google Calendar**: Not connected — saved in MyPA only (send /connect to sync future events)\n")
	case gState == googleFailed:
		sb.WriteString("• **Google Calendar**: ⚠️ Sync failed — saved in MyPA only\n")
	default:
		sb.WriteString("• **Google Calendar**: Not configured — saved in MyPA only\n")
	}

	if ev.IsShared {
		switch {
		case ev.PartnerGoogleEventID != "":
			sb.WriteString("• **Partner Calendar**: Synced ✅\n")
		case partnerFailed:
			sb.WriteString("• **Partner Calendar**: ⚠️ Sync failed — shared in the family space only\n")
		default:
			sb.WriteString("• **Partner Calendar**: Shared in family space\n")
		}
	}

	return sb.String(), nil
}

// matchesQuery is the free-text filter for MyPA's own events (Google filters its own).
func matchesQuery(e models.ScheduleEvent, query string) bool {
	q := strings.ToLower(query)
	return strings.Contains(strings.ToLower(e.Title), q) || strings.Contains(strings.ToLower(e.Description), q)
}

func (m *Module) executeListEvents(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	topic := strings.ToLower(strings.TrimSpace(getStringArg(args, "topic")))
	tagFilter := getStringSliceArg(args, "tags")
	query := strings.TrimSpace(getStringArg(args, "query"))
	includeGoogle := getBoolArg(args, "include_google", true)

	startTimeStr := strings.TrimSpace(getStringArg(args, "start_time"))
	var startTime time.Time
	if startTimeStr != "" {
		startTime, _ = parseFlexibleTime(startTimeStr)
	}
	if startTime.IsZero() {
		startTime = time.Now().Truncate(24 * time.Hour)
	}

	endTimeStr := strings.TrimSpace(getStringArg(args, "end_time"))
	var endTime time.Time
	if endTimeStr != "" {
		endTime, _ = parseFlexibleTime(endTimeStr)
	}
	if endTime.IsZero() {
		endTime = startTime.Add(30 * 24 * time.Hour)
	}

	userID := callerID(callCtx)

	// The household's shared events plus the caller's own (V4-30: scoped by household ID).
	eventsList, err := repo.ListScheduleEvents(callCtx.Scope, topic, startTime, endTime)
	if err != nil {
		return "", fmt.Errorf("failed to query schedule events: %w", err)
	}

	// Filter by tags and free text if specified
	if len(tagFilter) > 0 || query != "" {
		var filtered []models.ScheduleEvent
		for _, e := range eventsList {
			if len(tagFilter) > 0 && !hasAnyTag(e.Tags(), tagFilter) {
				continue
			}
			if query != "" && !matchesQuery(e, query) {
				continue
			}
			filtered = append(filtered, e)
		}
		eventsList = filtered
	}

	// Merge with Google Calendar events if enabled
	gState := googleOff
	if includeGoogle {
		var calClient orchestrator.CalendarClient
		calClient, gState = m.googleCalendar(ctx, callCtx, userID, "list")
		if calClient != nil {
			gEvents, err := calClient.ListEvents(ctx, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339), query)
			if err != nil {
				logGoogleFailure("list", userID, err)
				gState = googleFailed
			} else {
				// Rows written before V4-08 hold the web link, newer rows the ID: match both.
				knownIDs := make(map[string]bool)
				for _, e := range eventsList {
					for _, ref := range []string{e.ExternalGoogleEventID, e.PartnerGoogleEventID} {
						if ref != "" {
							knownIDs[ref] = true
							knownIDs[googleEventRef(ref)] = true
						}
					}
				}
				delete(knownIDs, "")

				for _, ge := range gEvents {
					if ge == nil || knownIDs[ge.Id] || knownIDs[ge.HtmlLink] {
						continue
					}

					// Extract topic from [Topic] prefix if present
					summary := strings.TrimSpace(ge.Summary)
					evTopic := generalTopic
					if strings.HasPrefix(summary, "[") && strings.Contains(summary, "]") {
						endIdx := strings.Index(summary, "]")
						evTopic = strings.ToLower(strings.TrimSpace(summary[1:endIdx]))
					}

					if topic != "" && !strings.EqualFold(topic, "all") && !strings.EqualFold(topic, evTopic) {
						continue
					}

					var gStart, gEnd time.Time
					if ge.Start != nil {
						if ge.Start.DateTime != "" {
							gStart, _ = time.Parse(time.RFC3339, ge.Start.DateTime)
						} else if ge.Start.Date != "" {
							gStart, _ = time.Parse("2006-01-02", ge.Start.Date)
						}
					}
					if ge.End != nil {
						if ge.End.DateTime != "" {
							gEnd, _ = time.Parse(time.RFC3339, ge.End.DateTime)
						} else if ge.End.Date != "" {
							gEnd, _ = time.Parse("2006-01-02", ge.End.Date)
						}
					}

					eventsList = append(eventsList, models.ScheduleEvent{
						ExternalGoogleEventID: ge.Id,
						UserID:                userID,
						Topic:                 evTopic,
						Title:                 summary,
						Description:           ge.Description,
						StartTime:             gStart,
						EndTime:               gEnd,
						SyncToGoogle:          true,
					})
				}
			}
		}
	}

	// Sort merged list by StartTime
	sort.Slice(eventsList, func(i, j int) bool {
		return eventsList[i].StartTime.Before(eventsList[j].StartTime)
	})

	if len(eventsList) == 0 {
		topicLabel := topic
		if topicLabel == "" {
			topicLabel = "all"
		}
		return withGoogleNote(fmt.Sprintf("No scheduled events found for topic '%s' between %s and %s.", topicLabel, startTime.Format("Jan 02"), endTime.Format("Jan 02")), gState), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📅 **Schedule Events (%d found)**:\n\n", len(eventsList)))
	for i, e := range eventsList {
		status := "Internal (DB)"
		if e.SyncToGoogle {
			status = "Google Synced"
		}
		if e.ID == 0 {
			status = "Google Calendar only"
		}
		if e.PartnerGoogleEventID != "" {
			status += " + Partner Synced"
		}

		sb.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, e.Title))
		sb.WriteString(fmt.Sprintf("   • **Topic**: `%s` | **Status**: %s\n", e.Topic, status))
		sb.WriteString(fmt.Sprintf("   • **When**: %s – %s\n", e.StartTime.Format("Mon, Jan 02 15:04"), e.EndTime.Format("15:04 MST")))
		if tags := e.Tags(); len(tags) > 0 {
			sb.WriteString(fmt.Sprintf("   • **Tags**: %s\n", strings.Join(tags, ", ")))
		}
		if e.Description != "" {
			sb.WriteString(fmt.Sprintf("   • **Notes**: %s\n", e.Description))
		}
		switch {
		case e.ID > 0:
			sb.WriteString(fmt.Sprintf("   • **ID**: `%d`\n", e.ID))
		case e.ExternalGoogleEventID != "":
			sb.WriteString(fmt.Sprintf("   • **Google ID**: `%s`\n", e.ExternalGoogleEventID))
		}
		sb.WriteString("\n")
	}

	return withGoogleNote(strings.TrimSpace(sb.String()), gState), nil
}

func (m *Module) executeConfigureTopicCalendar(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	topic := strings.ToLower(strings.TrimSpace(getStringArg(args, "topic")))
	if topic == "" {
		return "", fmt.Errorf("topic is required")
	}

	userID := callerID(callCtx)

	cfg, _ := repo.GetTopicCalendarConfig(userID, topic)
	// BL-68: whether the topic was configured, and how, decides how undo reverses this.
	action, beforeJSON := "create", ""
	if cfg != nil {
		b, _ := json.Marshal(models.SnapshotMap(cfg))
		action, beforeJSON = "update", string(b)
	}
	if cfg == nil {
		cfg = &models.TopicCalendarConfig{
			UserID:                   userID,
			Topic:                    topic,
			PrefixTag:                strings.ToUpper(topic[:1]) + strings.ToLower(topic[1:]),
			TargetCalendarID:         "primary",
			IncludeInMorningBriefing: true,
			UpdatedAt:                time.Now(),
		}
	}

	if prefix := strings.TrimSpace(getStringArg(args, "prefix_tag")); prefix != "" {
		cfg.PrefixTag = prefix
	}
	if color := strings.TrimSpace(getStringArg(args, "color_id")); color != "" {
		cfg.ColorID = color
	}
	if targetCal := strings.TrimSpace(getStringArg(args, "target_calendar_id")); targetCal != "" {
		cfg.TargetCalendarID = targetCal
	}
	if val, ok := args["sync_to_google_default"].(bool); ok {
		cfg.SyncToGoogleDefault = &val
	}
	if val, ok := args["include_in_morning_briefing"].(bool); ok {
		cfg.IncludeInMorningBriefing = val
	}

	if err := repo.UpsertTopicCalendarConfig(cfg); err != nil {
		return "", fmt.Errorf("failed to save topic calendar config: %w", err)
	}

	// Emit MutationEvent
	bus := m.resolveEventBus(callCtx)
	if bus != nil {
		afterBytes, _ := json.Marshal(cfg)
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "schedule",
			Action:      action,
			Kind:        "configure",
			EntityType:  "TopicCalendarConfig",
			EntityID:    strconv.FormatUint(uint64(cfg.ID), 10),
			ActorUserID: userID,
			// V4-30: without the household the mutation log drops the event, so it could not be undone.
			HouseholdID: callCtx.Scope.HouseholdID(),
			FamilyGroup: callCtx.Scope.LegacyLabel(),
			BeforeJSON:  beforeJSON,
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Configured virtual sub-calendar for topic '%s' (Prefix: [%s], Color: %s)", cfg.Topic, cfg.PrefixTag, cfg.ColorID),
			Timestamp:   time.Now(),
		})
	}

	syncDefaultStr := "Google Calendar Synced"
	if cfg.SyncToGoogleDefault != nil && !*cfg.SyncToGoogleDefault {
		syncDefaultStr = "Internal Database Only"
	}

	return fmt.Sprintf("✅ **Topic Calendar Configured for '%s'**:\n• **Prefix Tag**: `[%s]`\n• **Color ID**: `%s`\n• **Target Calendar**: `%s`\n• **Default Sync**: %s\n• **Morning Briefing**: %t",
		cfg.Topic, cfg.PrefixTag, cfg.ColorID, cfg.TargetCalendarID, syncDefaultStr, cfg.IncludeInMorningBriefing), nil
}

// googleOnlyClient is the caller's Google Calendar for an event that exists only in Google, or
// the reply explaining why there is none.
func (m *Module) googleOnlyClient(ctx context.Context, callCtx orchestrator.CallContext, op string) (orchestrator.CalendarClient, string) {
	client, st := m.googleCalendar(ctx, callCtx, callerID(callCtx), op)
	switch st {
	case googleOK:
		return client, ""
	case googleNotConnected:
		return nil, msgGoogleNotConnected
	case googleFailed:
		return nil, msgGoogleExpired
	default:
		return nil, msgGoogleNotConfigured
	}
}

// publishGoogleOnly records a change to an event MyPA does not store. It is not undoable: there
// is no row to restore (CalendarEvent is not in the rollback allowlist).
func (m *Module) publishGoogleOnly(ctx context.Context, callCtx orchestrator.CallContext, action, googleID, afterJSON, summary string) {
	_ = toolutil.PublishMutation(ctx, m.resolveEventBus(callCtx), events.MutationEvent{
		Topic:       "schedule",
		Action:      action,
		EntityType:  "CalendarEvent",
		EntityID:    googleID,
		ActorUserID: callerID(callCtx),
		HouseholdID: callCtx.Scope.HouseholdID(),
		FamilyGroup: callCtx.Scope.LegacyLabel(),
		AfterJSON:   afterJSON,
		Summary:     summary,
	})
}

func (m *Module) deleteGoogleOnlyEvent(ctx context.Context, callCtx orchestrator.CallContext, googleID string) (string, error) {
	client, reply := m.googleOnlyClient(ctx, callCtx, "delete")
	if client == nil {
		return reply, nil
	}
	if err := client.DeleteEvent(ctx, googleID); err != nil {
		logGoogleFailure("delete", callerID(callCtx), err)
		return "❌ Failed to delete event: " + err.Error(), nil
	}
	m.publishGoogleOnly(ctx, callCtx, "delete", googleID, "", fmt.Sprintf("Deleted calendar event '%s'", googleID))
	return "Event Deleted Successfully!", nil
}

func (m *Module) updateGoogleOnlyEvent(ctx context.Context, callCtx orchestrator.CallContext, googleID string, args map[string]interface{}) (string, error) {
	loc, err := locationArg(args)
	if err != nil {
		return "", err
	}
	patch := models.CalendarEvent{
		ID:          googleID,
		Title:       strings.TrimSpace(getStringArg(args, "title")),
		Description: strings.TrimSpace(getStringArg(args, "description")),
		Recurrence:  getRecurrenceArg(args),
	}
	for key, dst := range map[string]*string{"start_time": &patch.StartTime, "end_time": &patch.EndTime} {
		raw := strings.TrimSpace(getStringArg(args, key))
		if raw == "" {
			continue
		}
		t, err := parseFlexibleTimeIn(raw, loc)
		if err != nil {
			return "", fmt.Errorf("invalid %s %q: %w", key, raw, err)
		}
		*dst = t.In(loc).Format(time.RFC3339)
		patch.Timezone = loc.String()
	}

	client, reply := m.googleOnlyClient(ctx, callCtx, "update")
	if client == nil {
		return reply, nil
	}
	if err := client.UpdateEvent(ctx, googleID, patch); err != nil {
		logGoogleFailure("update", callerID(callCtx), err)
		return "❌ Failed to update event: " + err.Error(), nil
	}
	after, _ := json.Marshal(patch)
	m.publishGoogleOnly(ctx, callCtx, "update", googleID, string(after), fmt.Sprintf("Updated calendar event '%s'", googleID))
	return "Event Updated Successfully!", nil
}

func (m *Module) executeDeleteEvent(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	eventID, googleID, err := eventRefArg(args)
	if err != nil {
		return "", err
	}
	if googleID != "" {
		return m.deleteGoogleOnlyEvent(ctx, callCtx, googleID)
	}

	existing, err := repo.GetScheduleEvent(callCtx.Scope, eventID)
	if err != nil {
		return "", fmt.Errorf("failed to lookup event %d: %w", eventID, err)
	}
	// Defence in depth: the repository only finds the household's own events (V4-30).
	isAuthorized := existing != nil && callCtx.Scope.Valid() && existing.HouseholdID == callCtx.Scope.HouseholdID()
	if !isAuthorized {
		return fmt.Sprintf("Event with ID %d not found.", eventID), nil
	}

	// Delete from Google Calendar if synced; a copy we could not remove is reported (BL-1).
	googleCopyLeft := false
	if existing.ExternalGoogleEventID != "" {
		calClient, st := m.googleCalendar(ctx, callCtx, existing.UserID, "delete")
		if calClient != nil {
			if ref := googleEventRef(existing.ExternalGoogleEventID); ref == "" {
				googleCopyLeft = true
			} else if err := calClient.DeleteEvent(ctx, ref); err != nil {
				logGoogleFailure("delete", existing.UserID, err)
				googleCopyLeft = true
			}
		} else if st == googleNotConnected || st == googleFailed {
			googleCopyLeft = true
		}
	}

	// Delete from Partner Google Calendar if synced
	partnerCopyLeft := false
	if existing.PartnerGoogleEventID != "" {
		partnerUsers := m.findPartnerUsers(ctx, callCtx, existing.UserID)
		for _, partner := range partnerUsers {
			partnerCal, st := m.googleCalendar(ctx, callCtx, partner.PlatformID, "delete_partner")
			if st == googleFailed {
				partnerCopyLeft = true
			}
			if partnerCal != nil {
				if ref := googleEventRef(existing.PartnerGoogleEventID); ref == "" {
					partnerCopyLeft = true
				} else if err := partnerCal.DeleteEvent(ctx, ref); err != nil {
					logGoogleFailure("delete_partner", partner.PlatformID, err)
					partnerCopyLeft = true
				} else {
					partnerCopyLeft = false
				}
				break
			}
		}
	}

	if err := repo.DeleteScheduleEvent(callCtx.Scope, eventID); err != nil {
		return "", fmt.Errorf("failed to delete schedule event %d: %w", eventID, err)
	}

	bus := m.resolveEventBus(callCtx)
	if bus != nil {
		beforeBytes, _ := json.Marshal(models.SnapshotMap(existing))
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "schedule",
			Action:      "delete",
			EntityType:  "ScheduleEvent",
			EntityID:    strconv.FormatUint(uint64(eventID), 10),
			ActorUserID: existing.UserID,
			HouseholdID: existing.HouseholdID,
			FamilyGroup: existing.FamilyGroup,
			BeforeJSON:  string(beforeBytes),
			Summary:     fmt.Sprintf("Deleted schedule event '%s' (topic: %s)", existing.Title, existing.Topic),
			Timestamp:   time.Now(),
		})
	}

	res := fmt.Sprintf("🗑️ Schedule event '%s' (ID: %d) successfully deleted.", existing.Title, eventID)
	if googleCopyLeft {
		res += "\n⚠️ The copy could not be removed from Google Calendar — please delete it there manually."
	}
	if partnerCopyLeft {
		res += "\n⚠️ The copy could not be removed from your partner's Google Calendar — please delete it there manually."
	}
	return res, nil
}

func (m *Module) executeUpdateEvent(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	eventID, googleID, err := eventRefArg(args)
	if err != nil {
		return "", err
	}
	if googleID != "" {
		return m.updateGoogleOnlyEvent(ctx, callCtx, googleID, args)
	}
	loc, err := locationArg(args)
	if err != nil {
		return "", err
	}

	existing, err := repo.GetScheduleEvent(callCtx.Scope, eventID)
	if err != nil {
		return "", fmt.Errorf("failed to lookup event %d: %w", eventID, err)
	}
	// Defence in depth: the repository only finds the household's own events (V4-30).
	isAuthorized := existing != nil && callCtx.Scope.Valid() && existing.HouseholdID == callCtx.Scope.HouseholdID()
	if !isAuthorized {
		return fmt.Sprintf("Event with ID %d not found.", eventID), nil
	}

	beforeBytes, _ := json.Marshal(models.SnapshotMap(existing))

	if title := strings.TrimSpace(getStringArg(args, "title")); title != "" {
		existing.Title = title
	}
	if startStr := strings.TrimSpace(getStringArg(args, "start_time")); startStr != "" {
		if st, err := parseFlexibleTimeIn(startStr, loc); err == nil {
			existing.StartTime = st
		}
	}
	if endStr := strings.TrimSpace(getStringArg(args, "end_time")); endStr != "" {
		if et, err := parseFlexibleTimeIn(endStr, loc); err == nil {
			existing.EndTime = et
		}
	}
	if desc := strings.TrimSpace(getStringArg(args, "description")); desc != "" {
		existing.Description = desc
	}
	if tags := getStringSliceArg(args, "tags"); len(tags) > 0 {
		_ = existing.SetTags(tags)
	}
	recurrence := getRecurrenceArg(args)
	if len(recurrence) > 0 {
		existing.RecurrenceRule = strings.Join(recurrence, "\n")
	}

	// Update on Google Calendar if event was synced; a stale Google copy is reported (BL-1).
	googleStale := false
	if existing.ExternalGoogleEventID != "" {
		calClient, st := m.googleCalendar(ctx, callCtx, existing.UserID, "update")
		if calClient != nil {
			patch := googleEvent(calendarTitle(existing, ""), existing.Description, existing.StartTime, existing.EndTime, loc, recurrence)
			if ref := googleEventRef(existing.ExternalGoogleEventID); ref == "" {
				googleStale = true
			} else if err := calClient.UpdateEvent(ctx, ref, patch); err != nil {
				logGoogleFailure("update", existing.UserID, err)
				googleStale = true
			}
		} else if st == googleNotConnected || st == googleFailed {
			googleStale = true
		}
	}

	if err := repo.UpdateScheduleEvent(callCtx.Scope, existing); err != nil {
		return "", fmt.Errorf("failed to update event: %w", err)
	}

	bus := m.resolveEventBus(callCtx)
	if bus != nil {
		afterBytes, _ := json.Marshal(existing)
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "schedule",
			Action:      "update",
			EntityType:  "ScheduleEvent",
			EntityID:    strconv.FormatUint(uint64(eventID), 10),
			ActorUserID: existing.UserID,
			HouseholdID: existing.HouseholdID,
			FamilyGroup: existing.FamilyGroup,
			BeforeJSON:  string(beforeBytes),
			AfterJSON:   string(afterBytes),
			Summary:     fmt.Sprintf("Updated schedule event '%s' (topic: %s)", existing.Title, existing.Topic),
			Timestamp:   time.Now(),
		})
	}

	res := fmt.Sprintf("✅ Schedule event '%s' (ID: %d) updated.", existing.Title, eventID)
	if googleStale {
		res += "\n⚠️ The change was not updated in Google Calendar — the copy there still shows the old details."
	}
	return res, nil
}
