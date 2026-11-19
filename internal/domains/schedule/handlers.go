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
	"github.com/nivik/mypa/internal/events"
	"github.com/nivik/mypa/internal/models"
	"github.com/nivik/mypa/internal/orchestrator"
)

// Execute dispatches and runs tools provided by the schedule module.
func (m *Module) Execute(ctx context.Context, callCtx orchestrator.CallContext, toolName string, args map[string]interface{}) (string, error) {
	repo, err := m.resolveRepo(callCtx)
	if err != nil {
		return "", err
	}

	switch toolName {
	case "schedule_topic_event":
		return m.executeScheduleTopicEvent(ctx, callCtx, repo, args)
	case "list_topic_schedule":
		return m.executeListTopicSchedule(ctx, callCtx, repo, args)
	case "configure_topic_calendar":
		return m.executeConfigureTopicCalendar(ctx, callCtx, repo, args)
	case "delete_topic_event":
		return m.executeDeleteTopicEvent(ctx, callCtx, repo, args)
	case "update_topic_event":
		return m.executeUpdateTopicEvent(ctx, callCtx, repo, args)
	default:
		return "", fmt.Errorf("schedule: unknown tool %q", toolName)
	}
}

func (m *Module) executeScheduleTopicEvent(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	title := strings.TrimSpace(getStringArg(args, "title"))
	if title == "" {
		return "", fmt.Errorf("title is required")
	}

	topic := strings.ToLower(strings.TrimSpace(getStringArg(args, "topic")))
	if topic == "" {
		topic = "general"
	}

	startTimeStr := strings.TrimSpace(getStringArg(args, "start_time"))
	if startTimeStr == "" {
		return "", fmt.Errorf("start_time is required")
	}
	startTime, err := parseFlexibleTime(startTimeStr)
	if err != nil {
		return "", fmt.Errorf("invalid start_time %q: %w", startTimeStr, err)
	}

	endTimeStr := strings.TrimSpace(getStringArg(args, "end_time"))
	var endTime time.Time
	if endTimeStr != "" {
		endTime, err = parseFlexibleTime(endTimeStr)
		if err != nil {
			return "", fmt.Errorf("invalid end_time %q: %w", endTimeStr, err)
		}
	} else {
		endTime = startTime.Add(1 * time.Hour)
	}

	description := strings.TrimSpace(getStringArg(args, "description"))
	tags := getStringSliceArg(args, "tags")
	recurrence := strings.TrimSpace(getStringArg(args, "recurrence"))

	// Resolve user identity
	userID := ""
	familyGroup := ""
	if callCtx.User != nil {
		userID = callCtx.User.PlatformID
		if userID == "" {
			userID = callCtx.User.ID
		}
		familyGroup = callCtx.User.FamilyGroup
	}
	if userID == "" {
		userID = callCtx.Message.Sender
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
		FamilyGroup:     familyGroup,
		Topic:           topic,
		Title:           title,
		Description:     description,
		StartTime:       startTime,
		EndTime:         endTime,
		RecurrenceRule:  recurrence,
		SyncToGoogle:    syncToGoogle,
		IsShared:        isShared,
		CreatedByUserID: userID,
		CreatedAt:       time.Now(),
	}
	_ = ev.SetTags(tags)

	formattedTitle := ev.FormattedTitle(prefixTag)

	// Virtual Sub-Calendar: Google Calendar sync for primary user
	gState := googleOff
	if syncToGoogle {
		var calClient orchestrator.CalendarClient
		calClient, gState = m.googleCalendar(ctx, callCtx, userID, "create")
		if calClient != nil {
			calEv := models.CalendarEvent{
				Title:       formattedTitle,
				Description: description,
				StartTime:   startTime.Format(time.RFC3339),
				EndTime:     endTime.Format(time.RFC3339),
				Timezone:    "UTC",
			}
			if recurrence != "" {
				calEv.Recurrence = []string{recurrence}
			}

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
		partnerUsers := m.findPartnerUsers(callCtx, userID, familyGroup)
		for _, partner := range partnerUsers {
			partnerCal, pState := m.googleCalendar(ctx, callCtx, partner.PlatformID, "create_partner")
			if pState == googleFailed {
				partnerFailed = true
			}
			if partnerCal != nil {
				calEv := models.CalendarEvent{
					Title:       formattedTitle,
					Description: description,
					StartTime:   startTime.Format(time.RFC3339),
					EndTime:     endTime.Format(time.RFC3339),
					Timezone:    "UTC",
				}
				if recurrence != "" {
					calEv.Recurrence = []string{recurrence}
				}

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
	if err := repo.CreateScheduleEvent(ev); err != nil {
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
			FamilyGroup: familyGroup,
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
	sb.WriteString(fmt.Sprintf("• **Start**: %s\n", ev.StartTime.Format(time.RFC1123)))
	sb.WriteString(fmt.Sprintf("• **End**: %s\n", ev.EndTime.Format(time.RFC1123)))

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

func (m *Module) executeListTopicSchedule(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	topic := strings.ToLower(strings.TrimSpace(getStringArg(args, "topic")))
	tagFilter := getStringSliceArg(args, "tags")
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

	userID := ""
	familyGroup := ""
	if callCtx.User != nil {
		userID = callCtx.User.PlatformID
		if userID == "" {
			userID = callCtx.User.ID
		}
		familyGroup = callCtx.User.FamilyGroup
	}
	if userID == "" {
		userID = callCtx.Message.Sender
	}

	eventsList, err := repo.ListScheduleEvents(userID, familyGroup, topic, startTime, endTime)
	if err != nil {
		return "", fmt.Errorf("failed to query schedule events: %w", err)
	}

	// Filter by tags if specified
	if len(tagFilter) > 0 {
		var filtered []models.ScheduleEvent
		for _, e := range eventsList {
			eventTags := e.Tags()
			if hasAnyTag(eventTags, tagFilter) {
				filtered = append(filtered, e)
			}
		}
		eventsList = filtered
	}

	// Merge with Google Calendar events if enabled
	gState := googleOff
	if includeGoogle {
		var calClient orchestrator.CalendarClient
		calClient, gState = m.googleCalendar(ctx, callCtx, userID, "list")
		if calClient != nil {
			gEvents, err := calClient.ListEvents(ctx, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339), "")
			if err != nil {
				logGoogleFailure("list", userID, err)
				gState = googleFailed
			} else {
				knownIDs := make(map[string]bool)
				for _, e := range eventsList {
					if e.ExternalGoogleEventID != "" {
						knownIDs[e.ExternalGoogleEventID] = true
					}
					if e.PartnerGoogleEventID != "" {
						knownIDs[e.PartnerGoogleEventID] = true
					}
				}

				for _, ge := range gEvents {
					if ge == nil || knownIDs[ge.Id] || knownIDs[ge.HtmlLink] {
						continue
					}

					// Extract topic from [Topic] prefix if present
					summary := strings.TrimSpace(ge.Summary)
					evTopic := "general"
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
		if e.ID > 0 {
			sb.WriteString(fmt.Sprintf("   • **ID**: `%d`\n", e.ID))
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

	userID := ""
	if callCtx.User != nil {
		userID = callCtx.User.PlatformID
		if userID == "" {
			userID = callCtx.User.ID
		}
	}
	if userID == "" {
		userID = callCtx.Message.Sender
	}

	cfg, _ := repo.GetTopicCalendarConfig(userID, topic)
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
			Action:      "configure",
			EntityType:  "TopicCalendarConfig",
			EntityID:    strconv.FormatUint(uint64(cfg.ID), 10),
			ActorUserID: userID,
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

func (m *Module) executeDeleteTopicEvent(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	eventIDRaw, ok := getIntArg(args, "event_id")
	if !ok || eventIDRaw <= 0 {
		return "", fmt.Errorf("valid event_id is required")
	}
	eventID := uint(eventIDRaw)

	familyGroup := callCtx.TenantFamilyGroup()
	userID := callCtx.ActorID()
	existing, err := repo.GetScheduleEvent(eventID)
	if err != nil {
		return "", fmt.Errorf("failed to lookup event %d: %w", eventID, err)
	}
	isAuthorized := existing != nil && ((familyGroup != "" && existing.FamilyGroup == familyGroup) || (existing.UserID != "" && existing.UserID == userID))
	if !isAuthorized {
		return fmt.Sprintf("Event with ID %d not found.", eventID), nil
	}

	// Delete from Google Calendar if synced; a copy we could not remove is reported (BL-1).
	googleCopyLeft := false
	if existing.ExternalGoogleEventID != "" {
		calClient, st := m.googleCalendar(ctx, callCtx, existing.UserID, "delete")
		if calClient != nil {
			if err := calClient.DeleteEvent(ctx, existing.ExternalGoogleEventID); err != nil {
				logGoogleFailure("delete", existing.UserID, err)
				googleCopyLeft = true
			}
		} else if st == googleNotConnected || st == googleFailed {
			googleCopyLeft = true
		}
	}

	// Delete from Partner Google Calendar if synced
	partnerCopyLeft := false
	if existing.PartnerGoogleEventID != "" && existing.FamilyGroup != "" {
		partnerUsers := m.findPartnerUsers(callCtx, existing.UserID, existing.FamilyGroup)
		for _, partner := range partnerUsers {
			partnerCal, st := m.googleCalendar(ctx, callCtx, partner.PlatformID, "delete_partner")
			if st == googleFailed {
				partnerCopyLeft = true
			}
			if partnerCal != nil {
				if err := partnerCal.DeleteEvent(ctx, existing.PartnerGoogleEventID); err != nil {
					logGoogleFailure("delete_partner", partner.PlatformID, err)
					partnerCopyLeft = true
				} else {
					partnerCopyLeft = false
				}
				break
			}
		}
	}

	if err := repo.DeleteScheduleEvent(eventID); err != nil {
		return "", fmt.Errorf("failed to delete schedule event %d: %w", eventID, err)
	}

	bus := m.resolveEventBus(callCtx)
	if bus != nil {
		beforeBytes, _ := json.Marshal(existing)
		_ = bus.Publish(ctx, events.MutationEvent{
			Topic:       "schedule",
			Action:      "delete",
			EntityType:  "ScheduleEvent",
			EntityID:    strconv.FormatUint(uint64(eventID), 10),
			ActorUserID: existing.UserID,
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

func (m *Module) executeUpdateTopicEvent(ctx context.Context, callCtx orchestrator.CallContext, repo db.ScheduleRepository, args map[string]interface{}) (string, error) {
	eventIDRaw, ok := getIntArg(args, "event_id")
	if !ok || eventIDRaw <= 0 {
		return "", fmt.Errorf("valid event_id is required")
	}
	eventID := uint(eventIDRaw)

	familyGroup := callCtx.TenantFamilyGroup()
	userID := callCtx.ActorID()
	existing, err := repo.GetScheduleEvent(eventID)
	if err != nil {
		return "", fmt.Errorf("failed to lookup event %d: %w", eventID, err)
	}
	isAuthorized := existing != nil && ((familyGroup != "" && existing.FamilyGroup == familyGroup) || (existing.UserID != "" && existing.UserID == userID))
	if !isAuthorized {
		return fmt.Sprintf("Event with ID %d not found.", eventID), nil
	}

	beforeBytes, _ := json.Marshal(existing)

	if title := strings.TrimSpace(getStringArg(args, "title")); title != "" {
		existing.Title = title
	}
	if startStr := strings.TrimSpace(getStringArg(args, "start_time")); startStr != "" {
		if st, err := parseFlexibleTime(startStr); err == nil {
			existing.StartTime = st
		}
	}
	if endStr := strings.TrimSpace(getStringArg(args, "end_time")); endStr != "" {
		if et, err := parseFlexibleTime(endStr); err == nil {
			existing.EndTime = et
		}
	}
	if desc := strings.TrimSpace(getStringArg(args, "description")); desc != "" {
		existing.Description = desc
	}
	if tags := getStringSliceArg(args, "tags"); len(tags) > 0 {
		_ = existing.SetTags(tags)
	}

	// Update on Google Calendar if event was synced; a stale Google copy is reported (BL-1).
	googleStale := false
	if existing.ExternalGoogleEventID != "" {
		calClient, st := m.googleCalendar(ctx, callCtx, existing.UserID, "update")
		if calClient != nil {
			if err := calClient.UpdateEvent(ctx, existing.ExternalGoogleEventID, models.CalendarEvent{
				Title:       existing.FormattedTitle(""),
				Description: existing.Description,
				StartTime:   existing.StartTime.Format(time.RFC3339),
				EndTime:     existing.EndTime.Format(time.RFC3339),
				Timezone:    "UTC",
			}); err != nil {
				logGoogleFailure("update", existing.UserID, err)
				googleStale = true
			}
		} else if st == googleNotConnected || st == googleFailed {
			googleStale = true
		}
	}

	if err := repo.UpdateScheduleEvent(existing); err != nil {
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
