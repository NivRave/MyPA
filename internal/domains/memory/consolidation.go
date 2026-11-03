package memory

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nivik/mypa/internal/db"
	"github.com/nivik/mypa/internal/models"
)

// ConsolidationResult provides a detailed summary of actions taken during a memory sleep cycle.
type ConsolidationResult struct {
	MemoriesScanned      int           `json:"memories_scanned"`
	MemoriesDeduplicated int           `json:"memories_deduplicated"`
	MemoriesSuperseded   int           `json:"memories_superseded"`
	ProfilesUpdated      int           `json:"profiles_updated"`
	Details              []string      `json:"details"`
	Duration             time.Duration `json:"duration"`
}

// Single-value subtopics where a newer memory typically invalidates/supersedes an older memory.
var singleValueSubtopics = map[string]bool{
	"rent":              true,
	"address":           true,
	"landlord":          true,
	"lease_end":         true,
	"weight":            true,
	"height":            true,
	"current_weight":    true,
	"pediatrician":      true,
	"dob":               true,
	"birth_date":        true,
	"car_plate":         true,
	"insurance_expiry":  true,
	"license_plate":     true,
	"current_mileage":   true,
}

var keyValueRegex = regexp.MustCompile(`^([A-Za-z0-9_\-\s]{2,30})[:=]\s*(.+)$`)

// ConsolidateMemories performs the nightly or on-demand memory consolidation "sleep cycle".
// It deduplicates redundant topic memories, identifies and marks superseded facts,
// and aggregates structured facts into living EntityProfile cards.
func ConsolidateMemories(ctx context.Context, repo db.MemoryV2Repository) (*ConsolidationResult, error) {
	start := time.Now()
	result := &ConsolidationResult{
		Details: make([]string, 0),
	}

	memories, err := repo.ListActiveMemoriesForConsolidation(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch active memories for consolidation: %w", err)
	}

	result.MemoriesScanned = len(memories)
	if len(memories) == 0 {
		result.Duration = time.Since(start)
		return result, nil
	}

	// 1. Deduplication: Group memories by (FamilyGroup/UserID, Topic, Subtopic, Scope)
	grouped := make(map[string][]models.TopicMemory)
	for _, m := range memories {
		groupKey := fmt.Sprintf("%s|%s|%s|%s|%s", m.FamilyGroup, m.UserID, m.Topic, m.Subtopic, m.Scope)
		grouped[groupKey] = append(grouped[groupKey], m)
	}

	supersededIDs := make(map[uint]bool)

	for _, list := range grouped {
		if len(list) < 2 {
			continue
		}

		// Sort memories by CreatedAt ascending
		sort.Slice(list, func(i, j int) bool {
			return list[i].CreatedAt.Before(list[j].CreatedAt)
		})

		// Find duplicate facts
		factMap := make(map[string]models.TopicMemory)
		for _, mem := range list {
			normFact := strings.ToLower(strings.TrimSpace(mem.Fact))
			if existing, found := factMap[normFact]; found {
				// Supersede the older memory with this newer one
				if err := repo.SupersedeMemory(ctx, existing.ID, mem.ID); err == nil {
					supersededIDs[existing.ID] = true
					result.MemoriesDeduplicated++
					result.MemoriesSuperseded++
					result.Details = append(result.Details,
						fmt.Sprintf("Deduplicated identical fact: older memory #%d superseded by #%d (%s)",
							existing.ID, mem.ID, mem.Fact))
				}
			}
			factMap[normFact] = mem
		}

		// Check single-value subtopics (e.g. rent, weight, current address)
		subtopic := strings.ToLower(list[0].Subtopic)
		if singleValueSubtopics[subtopic] && len(list) >= 2 {
			// All older active memories in this subtopic should be superseded by the latest
			latest := list[len(list)-1]
			for i := 0; i < len(list)-1; i++ {
				older := list[i]
				if !supersededIDs[older.ID] && older.ID != latest.ID {
					if err := repo.SupersedeMemory(ctx, older.ID, latest.ID); err == nil {
						supersededIDs[older.ID] = true
						result.MemoriesSuperseded++
						result.Details = append(result.Details,
							fmt.Sprintf("Superseded single-value property [%s/%s]: older #%d replaced by latest #%d ('%s')",
								latest.Topic, subtopic, older.ID, latest.ID, latest.Fact))
					}
				}
			}
		}
	}

	// 2. Entity Profile Aggregation
	// Group active non-superseded memories by (FamilyGroup/UserID, Topic)
	type entityBucketKey struct {
		familyGroup string
		userID      string
		topic       string
	}
	topicBuckets := make(map[entityBucketKey][]models.TopicMemory)
	for _, m := range memories {
		if supersededIDs[m.ID] || !m.IsActive() {
			continue
		}
		key := entityBucketKey{
			familyGroup: m.FamilyGroup,
			userID:      m.UserID,
			topic:       m.Topic,
		}
		topicBuckets[key] = append(topicBuckets[key], m)
	}

	for key, mems := range topicBuckets {
		profilesMap := aggregateProfilesForTopic(key.familyGroup, key.userID, key.topic, mems)
		for _, prof := range profilesMap {
			if len(prof.Attributes()) == 0 {
				continue
			}
			if err := repo.UpsertEntityProfile(ctx, prof); err == nil {
				result.ProfilesUpdated++
				result.Details = append(result.Details,
					fmt.Sprintf("Aggregated entity profile: %s (%s • %s) with %d attributes",
						prof.EntityName, prof.EntityType, prof.Topic, len(prof.Attributes())))
			}
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// aggregateProfilesForTopic extracts key-value entity attributes from memories in a topic.
func aggregateProfilesForTopic(familyGroup, userID, topic string, mems []models.TopicMemory) map[string]*models.EntityProfile {
	profiles := make(map[string]*models.EntityProfile)

	// Sort memories chronologically so newer facts overwrite older attributes
	sort.Slice(mems, func(i, j int) bool {
		return mems[i].CreatedAt.Before(mems[j].CreatedAt)
	})

	for _, m := range mems {
		entityName, entityType, key, val := extractEntityAttribute(topic, m.Subtopic, m.Fact)
		if entityName == "" || key == "" || val == nil {
			continue
		}

		profileKey := fmt.Sprintf("%s|%s", entityType, strings.ToLower(entityName))
		prof, exists := profiles[profileKey]
		if !exists {
			prof = &models.EntityProfile{
				UserID:          userID,
				FamilyGroup:     familyGroup,
				Topic:           topic,
				EntityType:      entityType,
				EntityName:      entityName,
				UpdatedByUserID: m.CreatedByUserID,
				UpdatedAt:       time.Now(),
			}
			prof.SetAttributes(make(map[string]interface{}))
			profiles[profileKey] = prof
		}

		attrs := prof.Attributes()
		attrs[key] = val
		prof.SetAttributes(attrs)
		prof.UpdatedAt = time.Now()
	}

	return profiles
}

// extractEntityAttribute attempts to parse an entity name, entity type, attribute key and value
// from a memory fact and subtopic.
func extractEntityAttribute(topic, subtopic, fact string) (entityName, entityType, attrKey string, attrVal interface{}) {
	normTopic := strings.ToLower(topic)
	normSubtopic := strings.ToLower(subtopic)
	trimmedFact := strings.TrimSpace(fact)

	// Determine entity type based on topic
	switch normTopic {
	case "baby":
		entityType = models.EntityTypeBaby
		entityName = "Baby" // default if not specified
	case "house":
		entityType = models.EntityTypeProperty
		entityName = "Primary Residence"
	case "vehicle":
		entityType = models.EntityTypeVehicle
		entityName = "Vehicle"
	default:
		entityType = normTopic
		entityName = "General"
	}

	// 1. Try "key: value" or "Entity: key = value" pattern
	if matches := keyValueRegex.FindStringSubmatch(trimmedFact); len(matches) == 3 {
		parsedKey := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(matches[1]), " ", "_"))
		parsedVal := strings.TrimSpace(matches[2])
		return entityName, entityType, parsedKey, parsedVal
	}

	// 2. Use subtopic as attribute key if fact is a direct statement
	if normSubtopic != "" {
		attrKey = normSubtopic
		attrVal = trimmedFact

		// Check if entity name is mentioned in fact: e.g. "Liam's weight is 7.5kg"
		words := strings.Fields(trimmedFact)
		if len(words) > 0 && strings.HasSuffix(words[0], "'s") {
			name := strings.TrimSuffix(words[0], "'s")
			entityName = name
		}

		return entityName, entityType, attrKey, attrVal
	}

	return "", "", "", nil
}
