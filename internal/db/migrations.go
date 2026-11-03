package db

import (
	"sync"

	"github.com/nivik/mypa/internal/models"
)

var (
	registeredModelsMu sync.RWMutex
	registeredModels   []interface{}
)

// RegisterWave1Models explicitly registers all Wave 1 GORM models with the migration system.
func RegisterWave1Models() {
	RegisterModel(
		&models.EncryptedCredential{},
		&models.MutationLog{},
		&models.IdempotencyRecord{},
		&models.TopicMemory{},
		&models.EntityProfile{},
		&models.SharedSpace{},
		&models.SharedTaskReminder{},
		&models.ScheduleEvent{},
		&models.TopicCalendarConfig{},
	)
}

// RegisterModel registers one or more GORM models to be included in AutoMigrate.
// This allows domain tracks in subsequent waves to register their models in an isolated manner.
func RegisterModel(models ...interface{}) {
	registeredModelsMu.Lock()
	defer registeredModelsMu.Unlock()
	registeredModels = append(registeredModels, models...)
}

// GetRegisteredModels returns a thread-safe copy of all registered GORM models.
func GetRegisteredModels() []interface{} {
	registeredModelsMu.RLock()
	defer registeredModelsMu.RUnlock()
	res := make([]interface{}, len(registeredModels))
	copy(res, registeredModels)
	return res
}

// ResetRegisteredModels clears all registered models from memory (useful for isolated tests).
func ResetRegisteredModels() {
	registeredModelsMu.Lock()
	defer registeredModelsMu.Unlock()
	registeredModels = nil
}
