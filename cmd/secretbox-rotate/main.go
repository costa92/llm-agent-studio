// Command secretbox-rotate re-encrypts every secretbox-protected column from an
// OLD STUDIO_CONFIG_ENC_KEY to a NEW one (issue #22 方案 A: 一次性离线轮换脚本).
//
// The secretbox ciphertext format carries NO version/key-id header, so a key
// rotation cannot be done incrementally/online — there is no way to tell which
// rows are under which key. This tool does the only correct thing: a STOP-THE-
// WORLD, all-or-nothing batch re-encrypt. Run it during a maintenance window with
// the studiod processes stopped (so no row is written under the old key mid-run).
//
// It decrypts each row with the OLD key and re-encrypts with the NEW key inside a
// single transaction: if ANY row fails to decrypt (wrong OLD key) the whole run
// rolls back and nothing changes — there is no half-rotated state.
//
// Covered columns (every caller of secretbox.Box):
//   - model_configs.api_key_enc   (BYOK 模型密钥)
//   - storage_configs.secret_enc  (对象存储 secret)
//   - mail_configs.smtp_pass_enc  (SMTP 密码)
//
// Usage:
//
//	# dry-run (default): decrypt+re-encrypt in memory, roll back, report counts
//	PG_URL=postgres://... \
//	  secretbox-rotate -old-key "$OLD_B64" -new-key "$NEW_B64"
//
//	# commit: actually write the re-encrypted ciphertext
//	PG_URL=postgres://... \
//	  secretbox-rotate -old-key "$OLD_B64" -new-key "$NEW_B64" -commit
//
// Keys are base64-encoded 32-byte AES-256 keys, same format as
// STUDIO_CONFIG_ENC_KEY. -old-key/-new-key default to the env vars
// STUDIO_CONFIG_ENC_KEY_OLD / STUDIO_CONFIG_ENC_KEY_NEW when the flag is empty.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"

	"github.com/costa92/llm-agent-studio/internal/secretbox"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// encColumn names one secretbox-protected (table, id, column) triple.
type encColumn struct {
	table  string
	idCol  string
	encCol string
}

var encColumns = []encColumn{
	{"model_configs", "id", "api_key_enc"},
	{"storage_configs", "id", "secret_enc"},
	{"mail_configs", "id", "smtp_pass_enc"},
}

func main() {
	oldKey := flag.String("old-key", os.Getenv("STUDIO_CONFIG_ENC_KEY_OLD"), "base64 32B current key (decrypt); default env STUDIO_CONFIG_ENC_KEY_OLD")
	newKey := flag.String("new-key", os.Getenv("STUDIO_CONFIG_ENC_KEY_NEW"), "base64 32B new key (re-encrypt); default env STUDIO_CONFIG_ENC_KEY_NEW")
	pgURL := flag.String("pg-url", os.Getenv("PG_URL"), "postgres URL; default env PG_URL")
	commit := flag.Bool("commit", false, "write the re-encrypted rows; without it the run is a dry-run (rolled back)")
	flag.Parse()

	if err := run(context.Background(), *oldKey, *newKey, *pgURL, *commit); err != nil {
		fmt.Fprintf(os.Stderr, "secretbox-rotate: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, oldKeyB64, newKeyB64, pgURL string, commit bool) error {
	if oldKeyB64 == "" || newKeyB64 == "" {
		return fmt.Errorf("both -old-key and -new-key are required (base64 32B AES-256 keys)")
	}
	if oldKeyB64 == newKeyB64 {
		return fmt.Errorf("-old-key and -new-key are identical — nothing to rotate")
	}
	oldBox, err := secretbox.New(oldKeyB64)
	if err != nil {
		return fmt.Errorf("old key: %w", err)
	}
	newBox, err := secretbox.New(newKeyB64)
	if err != nil {
		return fmt.Errorf("new key: %w", err)
	}
	if !oldBox.Enabled() || !newBox.Enabled() {
		return fmt.Errorf("both keys must be non-empty 32-byte keys")
	}

	st, err := storage.Open(ctx, storage.Config{PGURL: pgURL})
	if err != nil {
		return err
	}
	defer st.Close()
	pool := st.Pool()

	// One transaction for the whole rotation: any decrypt failure (wrong old key)
	// aborts everything — no half-rotated state can persist.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	total := 0
	for _, c := range encColumns {
		n, err := rotateColumn(ctx, tx, oldBox, newBox, c)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", c.table, c.encCol, err)
		}
		fmt.Printf("  %-16s %-14s re-encrypted %d row(s)\n", c.table, c.encCol, n)
		total += n
	}

	if !commit {
		// Dry-run: prove every row decrypts+re-encrypts, then throw the work away.
		fmt.Printf("DRY-RUN: %d row(s) would be re-encrypted. Re-run with -commit to apply.\n", total)
		return nil // deferred Rollback discards the in-tx UPDATEs
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Printf("COMMITTED: %d row(s) re-encrypted with the new key.\n", total)
	return nil
}

// rotateColumn re-encrypts every non-null ciphertext in one (table, column) under
// the new key, in the given tx. Returns the number of rows rewritten.
func rotateColumn(ctx context.Context, tx pgx.Tx, oldBox, newBox *secretbox.Box, c encColumn) (int, error) {
	rows, err := tx.Query(ctx,
		fmt.Sprintf(`SELECT %s, %s FROM %s WHERE %s IS NOT NULL`, c.idCol, c.encCol, c.table, c.encCol))
	if err != nil {
		return 0, fmt.Errorf("select: %w", err)
	}
	type rewrite struct {
		id        string
		newCipher []byte
	}
	var pending []rewrite
	for rows.Next() {
		var id string
		var cipher []byte
		if err := rows.Scan(&id, &cipher); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan: %w", err)
		}
		if len(cipher) == 0 {
			continue // empty (not NULL) ciphertext: nothing to rotate
		}
		plain, err := oldBox.Decrypt(cipher)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("decrypt row %s with old key (wrong -old-key?): %w", id, err)
		}
		recipher, err := newBox.Encrypt(plain)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("re-encrypt row %s: %w", id, err)
		}
		pending = append(pending, rewrite{id: id, newCipher: recipher})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate: %w", err)
	}
	rows.Close()

	for _, r := range pending {
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`UPDATE %s SET %s=$1 WHERE %s=$2`, c.table, c.encCol, c.idCol),
			r.newCipher, r.id); err != nil {
			return 0, fmt.Errorf("update row %s: %w", r.id, err)
		}
	}
	return len(pending), nil
}
