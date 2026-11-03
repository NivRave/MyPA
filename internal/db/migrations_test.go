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

func TestRegisterModel_ThreadSafetyAndRetrieval(t *testing.T) {
	ResetRegisteredModels()
	defer ResetRegisteredModels()

	assert.Empty(t, GetRegisteredModels())

	RegisterModel(&dummyModelA{})
	models := GetRegisteredModels()
	assert.Len(t, models, 1)

	RegisterModel(&dummyModelB{})
	models = GetRegisteredModels()
	assert.Len(t, models, 2)

	// Verify mutating returned slice does not alter internal state
	models[0] = nil
	cleanModels := GetRegisteredModels()
	assert.NotNil(t, cleanModels[0])

	// Concurrent registration
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
	assert.Len(t, GetRegisteredModels(), goroutines)
}

func TestRegisterWave1Models(t *testing.T) {
	ResetRegisteredModels()
	defer ResetRegisteredModels()

	RegisterWave1Models()
	models := GetRegisteredModels()
	assert.Len(t, models, 9)
}

