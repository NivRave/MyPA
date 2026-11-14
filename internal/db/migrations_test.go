package db

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

type dummyModelA struct {
	ID   uint
	Name string
}

type dummyModelB struct {
	ID    uint
	Value int
}

func restoreRegisteredModels() {
	ResetRegisteredModels()
	RegisterWave1Models()
	RegisterWave2Models()
}

func TestRegisterModel_ThreadSafetyAndDeduplication(t *testing.T) {
	ResetRegisteredModels()
	defer restoreRegisteredModels()

	assert.Empty(t, GetRegisteredModels())

	RegisterModel(&dummyModelA{})
	models := GetRegisteredModels()
	assert.Len(t, models, 1)

	// Duplicate registration of dummyModelA should not increase count
	RegisterModel(&dummyModelA{})
	models = GetRegisteredModels()
	assert.Len(t, models, 1)

	RegisterModel(&dummyModelB{})
	models = GetRegisteredModels()
	assert.Len(t, models, 2)

	// Verify mutating returned slice does not alter internal state
	models[0] = nil
	cleanModels := GetRegisteredModels()
	assert.NotNil(t, cleanModels[0])

	// Concurrent registration with deduplication
	ResetRegisteredModels()
	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			RegisterModel(&dummyModelA{})
			_ = GetRegisteredModels()
		}()
	}

	wg.Wait()
	// With reflect.TypeOf deduplication, all 20 calls register the single unique type
	assert.Len(t, GetRegisteredModels(), 1)
}

func TestRegisterWave1Models(t *testing.T) {
	ResetRegisteredModels()
	defer restoreRegisteredModels()

	RegisterWave1Models()
	// Multiple registrations are idempotent
	RegisterWave1Models()
	models := GetRegisteredModels()
	assert.Len(t, models, 9)
}

func TestRegisterWave2Models(t *testing.T) {
	ResetRegisteredModels()
	defer restoreRegisteredModels()

	RegisterWave2Models()
	// Multiple registrations are idempotent
	RegisterWave2Models()
	models := GetRegisteredModels()
	assert.Len(t, models, 13)
}
