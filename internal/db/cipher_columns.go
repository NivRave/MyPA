package db

import (
	"context"
	"database/sql"
	"fmt"
)

// CipherColumn is one bytea column of encrypted values, as a store of the re-encryption pass
// (V4-07, reencrypt.Store). It reads and writes the stored bytes as they are; it never decrypts.
// The pass scans every household: it is an install-wide maintenance command, run by the operator.
type CipherColumn struct {
	db *sql.DB
	// name is what the report calls it.
	name string
	// table and column hold the values; keyExpr is a SQL expression of the row's primary key, as
	// text. All three are constants of this file, never input.
	table, column, keyExpr string
}

// CredentialFallbackStore is the credential fallback table: the encrypted copies of Google
// sign-ins (and other provider tokens) kept in Postgres.
func CredentialFallbackStore(sqlDB *sql.DB) CipherColumn {
	return CipherColumn{db: sqlDB, name: "credential fallback (encrypted_credentials)",
		table: "encrypted_credentials", column: "ciphertext", keyExpr: "user_id || '|' || provider"}
}

// ConnectorHeadersStore is the encrypted authentication headers of the API connectors.
func ConnectorHeadersStore(sqlDB *sql.DB) CipherColumn {
	return CipherColumn{db: sqlDB, name: "API connector headers (external_api_connectors)",
		table: "external_api_connectors", column: "encrypted_auth_headers", keyExpr: "id::text"}
}

// Name implements reencrypt.Store.
func (c CipherColumn) Name() string { return c.name }

// Each implements reencrypt.Store: every non-empty value, keyed by its row.
func (c CipherColumn) Each(ctx context.Context, fn func(key string, value []byte) error) error {
	rows, err := c.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT %[1]s, %[2]s FROM public.%[3]s WHERE %[2]s IS NOT NULL AND length(%[2]s) > 0 ORDER BY 1`,
		c.keyExpr, c.column, c.table))
	if err != nil {
		return fmt.Errorf("read %s: %w", c.table, err)
	}
	type row struct {
		key   string
		value []byte
	}
	var all []row // read first, so the writes below do not run inside the open query
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.key, &r.value); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read %s: %w", c.table, err)
		}
		all = append(all, r)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("read %s: %w", c.table, err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read %s: %w", c.table, err)
	}
	for _, r := range all {
		if err := fn(r.key, r.value); err != nil {
			return err
		}
	}
	return nil
}

// Replace implements reencrypt.Store: the row's value is replaced only while it still holds old.
// Nothing else in the row changes (updated_at included: the secret is the same).
func (c CipherColumn) Replace(ctx context.Context, key string, old, value []byte) (bool, error) {
	res, err := c.db.ExecContext(ctx, fmt.Sprintf(
		`UPDATE public.%[1]s SET %[2]s = $1 WHERE %[3]s = $2 AND %[2]s = $3`, c.table, c.column, c.keyExpr),
		value, key, old)
	if err != nil {
		return false, fmt.Errorf("rewrite a value in %s: %w", c.table, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
