package durability

import (
	"github.com/nivik/mypa/internal/db"
)

// AllowedRollbackEntities defines the complete, authoritative mapping of domain entity types to
// physical database tables: the undo allowlist, kept beside the rollback itself (db.UndoTables).
var AllowedRollbackEntities = db.UndoTables

// ResolveRollbackTable validates entityType against allowlist and returns the physical table name.
func ResolveRollbackTable(entityType string) (string, error) {
	return db.ResolveUndoTable(entityType)
}
