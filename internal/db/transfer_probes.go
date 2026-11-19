package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Read-only probes for the restore-and-migrate tool (V4-43). They take a plain *sql.DB because
// they also run right after a restore, before anything else has touched the database.

// CountCredentialCopies returns how many encrypted credentials of a provider the database holds
// (for example "google_oauth": the durable copies of Google sign-ins). A database without the
// table holds none.
func CountCredentialCopies(ctx context.Context, sqlDB *sql.DB, provider string) (int, error) {
	exists, err := tableExists(ctx, sqlDB, "encrypted_credentials")
	if err != nil || !exists {
		return 0, err
	}
	var n int
	if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM encrypted_credentials WHERE provider = $1`, provider).Scan(&n); err != nil {
		return 0, fmt.Errorf("count encrypted credentials: %w", err)
	}
	return n, nil
}

// CredentialCiphertexts returns up to limit stored ciphertexts, so the caller can test whether
// the install's data encryption key opens them. It returns ciphertext only: no user, no provider.
func CredentialCiphertexts(ctx context.Context, sqlDB *sql.DB, limit int) ([][]byte, error) {
	exists, err := tableExists(ctx, sqlDB, "encrypted_credentials")
	if err != nil || !exists {
		return nil, err
	}
	rows, err := sqlDB.QueryContext(ctx, `SELECT ciphertext FROM encrypted_credentials ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("read encrypted credentials: %w", err)
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var c []byte
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
