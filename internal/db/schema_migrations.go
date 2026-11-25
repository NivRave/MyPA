package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
)

// Versioned schema migrations (P4-3, goose). Production services never AutoMigrate: the schema
// is changed only by these migrations, run as an explicit step (`orchestrator -migrate-only`,
// the `migrate` compose service), and services refuse to start while migrations are pending.
//
// Adding a schema change: create internal/db/migrations/000NN_<name>.sql (or a Go migration
// below) AND update the GORM model. TestMigrations_FreshDatabaseMatchesModels fails if they drift.

//go:embed migrations/*.sql
var migrationFS embed.FS

const migrationTable = "goose_db_version"

// goMigrations are migrations implemented in Go (registered alongside the embedded SQL files).
func goMigrations() []*goose.Migration {
	return []*goose.Migration{
		// 2: copy the v1 `memories` rows into `topic_memories` (P2-4). Previously ran inside
		// NewClient on every start; idempotent, so it is safe on databases that already ran it.
		goose.NewGoMigration(2, &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, migration2SQL)
			if err != nil {
				return fmt.Errorf("copy v1 memories: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				slog.Info("copied v1 memories into topic_memories", "rows", n)
			}
			return nil
		}}, nil),
		// 4: Migration B of ADR 0002 (V4-32): people, channel accounts, memberships, access
		// tokens, and person_id beside every person column (person_keys.go). Additive only.
		goose.NewGoMigration(4, &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
			unmapped, err := migrationB(ctx, tx)
			if err != nil {
				return fmt.Errorf("migration B: %w", err)
			}
			logUnmappedPersonKeys(unmapped)
			return nil
		}}, nil),
		// 13: BL-26. save_topic_memory stored scope as the model sent it, and every read matches
		// "personal" or "family" exactly, so a memory saved as "Family" was found by nobody. Each
		// stored scope is corrected by the rule saves now apply (models.NormalizeMemoryScope). A
		// data fix: Down changes nothing, since the old spellings carry no meaning.
		goose.NewGoMigration(13, &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
			toFamily, toPersonal, err := normaliseMemoryScopes(ctx, tx)
			if err != nil {
				return fmt.Errorf("normalise memory scopes: %w", err)
			}
			slog.Info("memory scopes normalised", "to_family", toFamily, "to_personal", toPersonal)
			return nil
		}}, &goose.GoFunc{Mode: goose.TransactionEnabled}),
	}
}

// normaliseMemoryScopes is migration 13 (BL-26): a scope that is "family" after trimming and
// lower-casing becomes family; any other value that is not already "personal" or "family" becomes
// personal. The rule is applied in Go, with the same strings.TrimSpace and strings.ToLower saves
// use (models.NormalizeMemoryScope; written out here so the migration stays as it ran), so every
// kind of white space is trimmed as on a save. It reads only the ID and the scope of rows whose
// scope is not a literal, and returns how many rows each kind changed.
func normaliseMemoryScopes(ctx context.Context, tx *sql.Tx) (toFamily, toPersonal int64, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, scope FROM public.topic_memories
		WHERE scope IS NULL OR scope NOT IN ('personal', 'family')`)
	if err != nil {
		return 0, 0, err
	}
	var family, personal []int64
	for rows.Next() {
		var id int64
		var scope sql.NullString
		if err := rows.Scan(&id, &scope); err != nil {
			_ = rows.Close()
			return 0, 0, err
		}
		if strings.ToLower(strings.TrimSpace(scope.String)) == "family" {
			family = append(family, id)
		} else {
			personal = append(personal, id)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	for _, set := range []struct {
		scope string
		ids   []int64
	}{{"family", family}, {"personal", personal}} {
		for _, id := range set.ids {
			if _, err := tx.ExecContext(ctx, `UPDATE public.topic_memories SET scope = $1 WHERE id = $2`, set.scope, id); err != nil {
				return 0, 0, err
			}
		}
	}
	return int64(len(family)), int64(len(personal)), nil
}

func newMigrationProvider(sqlDB *sql.DB) (*goose.Provider, error) {
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	// A Postgres advisory lock serializes concurrent migrators (e.g. two replicas starting).
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, sqlDB, sub,
		goose.WithGoMigrations(goMigrations()...),
		goose.WithSessionLocker(locker),
	)
}

// Migrate brings the schema to the latest version. A pre-goose database (tables created by the
// old startup AutoMigrate, no goose version table) is first ADOPTED at the baseline (version 1)
// without executing it, then any later migrations run normally.
func Migrate(ctx context.Context, sqlDB *sql.DB) error {
	if err := adoptPreGooseSchema(ctx, sqlDB); err != nil {
		return fmt.Errorf("adopt existing schema: %w", err)
	}
	p, err := newMigrationProvider(sqlDB)
	if err != nil {
		return err
	}
	results, err := p.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, r := range results {
		slog.Info("applied migration", "version", r.Source.Version, "path", r.Source.Path, "duration", r.Duration)
	}
	return nil
}

// HasPendingMigrations reports whether the database is behind the embedded migrations.
func HasPendingMigrations(ctx context.Context, sqlDB *sql.DB) (bool, error) {
	if adopted, err := needsAdoption(ctx, sqlDB); err != nil {
		return false, err
	} else if adopted {
		return true, nil
	}
	p, err := newMigrationProvider(sqlDB)
	if err != nil {
		return false, err
	}
	return p.HasPending(ctx)
}

// EnsureMigrated returns an error (telling the operator what to run) unless the schema is current.
func EnsureMigrated(ctx context.Context, sqlDB *sql.DB) error {
	pending, err := HasPendingMigrations(ctx, sqlDB)
	if err != nil {
		return fmt.Errorf("check schema version: %w", err)
	}
	if pending {
		return fmt.Errorf("database schema is not up to date: run `mypa -migrate-only` " +
			"(docker compose runs it as the `migrate` service before the app starts)")
	}
	return nil
}

func tableExists(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, name string) (bool, error) {
	var exists bool
	err := q.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&exists)
	return exists, err
}

// needsAdoption: the app's tables exist (users is the oldest table) but goose has never run.
func needsAdoption(ctx context.Context, sqlDB *sql.DB) (bool, error) {
	hasVersions, err := tableExists(ctx, sqlDB, migrationTable)
	if err != nil || hasVersions {
		return false, err
	}
	return tableExists(ctx, sqlDB, "users")
}

func adoptPreGooseSchema(ctx context.Context, sqlDB *sql.DB) error {
	adopt, err := needsAdoption(ctx, sqlDB)
	if err != nil || !adopt {
		return err
	}
	store, err := database.NewStore(goose.DialectPostgres, migrationTable)
	if err != nil {
		return err
	}
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize with any concurrent adopter, then re-check inside the lock.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('mypa_goose_adopt'))`); err != nil {
		return err
	}
	if exists, err := tableExists(ctx, tx, migrationTable); err != nil || exists {
		return err
	}
	if err := store.CreateVersionTable(ctx, tx); err != nil {
		return err
	}
	for _, v := range []int64{0, 1} { // goose's initial row, then the baseline
		if err := store.Insert(ctx, tx, database.InsertRequest{Version: v}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	slog.Warn("adopted pre-goose database schema at baseline version 1 (baseline SQL not executed)")
	return nil
}
