package db

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm/schema"
)

// BL-68: every undoable table has its model in undoModels (what an older snapshot of it may lack),
// and that model is the table's.
func TestUndoModels_EveryUndoableTableHasItsModel(t *testing.T) {
	cache := &sync.Map{}
	for _, table := range UndoTables {
		model, ok := undoModels[table]
		if !assert.True(t, ok, "undoModels lacks %s", table) {
			continue
		}
		s, err := schema.Parse(model, cache, schema.NamingStrategy{})
		if assert.NoError(t, err, table) {
			assert.Equal(t, table, s.Table, "the model of %s", table)
		}
	}
	assert.Len(t, undoModels, 19)
}
