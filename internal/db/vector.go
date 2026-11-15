package db

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// halfvecCosineDistanceSQL must match the HNSW expression indexes created in migrations.go
// (`USING hnsw ((embedding::halfvec(3072)) halfvec_cosine_ops)`). Postgres only uses an
// expression index when the query contains the same expression; plain `embedding <=> ?` forces
// a sequential scan over every stored memory.
const halfvecCosineDistanceSQL = "embedding::halfvec(3072) <=> ?::halfvec(3072)"

// cosineOrder returns the ORDER BY clause for nearest-neighbour search on `embedding`.
//
// It MUST be a clause.OrderBy: GORM's Order() silently ignores clause.Expr / gorm.Expr values, so
// `Order(gorm.Expr("embedding <=> ?", v))` emitted no ORDER BY at all and "similarity search"
// returned the first N matching rows in physical order.
func cosineOrder(embedding pgvector.Vector) clause.OrderBy {
	return clause.OrderBy{Expression: clause.Expr{
		SQL:                halfvecCosineDistanceSQL,
		Vars:               []interface{}{embedding},
		WithoutParentheses: true,
	}}
}

// iterativeScanSupport caches, per connection pool, whether the installed pgvector supports
// hnsw.iterative_scan (>= 0.8.0).
var iterativeScanSupport sync.Map // map[*sql.DB]bool

func supportsIterativeScan(ctx context.Context, gdb *gorm.DB) bool {
	sqlDB, err := gdb.DB()
	if err != nil {
		return false
	}
	if v, ok := iterativeScanSupport.Load(sqlDB); ok {
		return v.(bool)
	}
	var version string
	if err := gdb.WithContext(ctx).Raw("SELECT extversion FROM pg_extension WHERE extname = 'vector'").Scan(&version).Error; err != nil {
		return false // transient error: don't cache, retry next time
	}
	ok := versionAtLeast(version, 0, 8)
	if !ok {
		slog.Warn("pgvector < 0.8: HNSW iterative scans unavailable; tenant-filtered vector search may miss rows once the index is used",
			"pgvector_version", version)
	}
	iterativeScanSupport.Store(sqlDB, ok)
	return ok
}

// withVectorScan runs a similarity query. With an HNSW index, Postgres fetches the ef_search
// nearest rows table-wide and applies WHERE filters afterwards, so a tenant-filtered search can
// return nothing when other tenants own the nearest rows. On pgvector >= 0.8 the query runs in a
// transaction with `hnsw.iterative_scan = strict_order`, which keeps scanning (in exact distance
// order) until LIMIT rows pass the filter. fn must build the entire query from tx.
func withVectorScan(ctx context.Context, gdb *gorm.DB, fn func(tx *gorm.DB) error) error {
	if !supportsIterativeScan(ctx, gdb) {
		return fn(gdb.WithContext(ctx))
	}
	return gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL hnsw.iterative_scan = strict_order").Error; err != nil {
			return err
		}
		return fn(tx)
	})
}

// versionAtLeast reports whether a dotted version string ("0.8.1") is >= major.minor.
func versionAtLeast(v string, major, minor int) bool {
	parts := strings.Split(strings.TrimSpace(v), ".")
	if len(parts) < 2 {
		return false
	}
	maj, err1 := strconv.Atoi(parts[0])
	mnr, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return maj > major || (maj == major && mnr >= minor)
}
