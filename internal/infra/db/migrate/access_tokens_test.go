package migrate

import "testing"

// 0021_access_tokens moves every personal access token into access_tokens
// unchanged (same id and hash, kind pat, scope full) and drops user_tokens;
// linked identities gain email and created_at.
func TestMigrate0021CopiesPATs(t *testing.T) {
	const (
		org   = "00000000-0000-7000-8000-000000000001"
		user  = "00000000-0000-7000-8000-000000000002"
		tokID = "00000000-0000-7000-8000-000000000003"
		hash  = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
		now   = "2026-10-14 12:00:00"
		exp   = "2027-10-14 12:00:00"
	)
	for _, d := range engines(t) {
		t.Run(d, func(t *testing.T) {
			ctx := tctx(t)
			db := openEmpty(t, d)
			if err := MigrateTo(ctx, db, d, 20); err != nil {
				t.Fatal(err)
			}
			mustExec(t, db, d, `INSERT INTO users (user_id, name) VALUES (?, 'u')`, user)
			mustExec(t, db, d, `INSERT INTO user_connected_accounts (provider, external_id, user_id) VALUES ('google', 'sub-1', ?)`, user)
			mustExec(t, db, d, `INSERT INTO user_tokens (token_id, user_id, name, token_prefix, token_hash, token_type, expires_at, last_used_at, created_at)
				VALUES (?, ?, 'ci', 'wfp_abcd1234', ?, 'pat', ?, ?, ?)`, tokID, user, hash, exp, now, now)

			if err := Migrate(ctx, db, d); err != nil {
				t.Fatal(err)
			}
			if n := count(t, db, d, `SELECT count(*) FROM access_tokens WHERE id = ? AND user_id = ? AND kind = 'pat' AND scope = 'full'
				AND token_hash = ? AND name = 'ci' AND token_prefix = 'wfp_abcd1234' AND expires_at IS NOT NULL
				AND last_used_at IS NOT NULL AND revoked_at IS NULL AND grant_id IS NULL`, tokID, user, hash); n != 1 {
				t.Fatalf("pat not copied unchanged: %d rows", n)
			}
			if n := count(t, db, d, `SELECT count(*) FROM access_tokens`); n != 1 {
				t.Fatalf("%d access_tokens rows, want 1", n)
			}
			if n := count(t, db, d, tableCountSQL(d), "user_tokens"); n != 0 {
				t.Fatal("user_tokens still exists")
			}
			if n := count(t, db, d, `SELECT count(*) FROM user_connected_accounts WHERE provider = 'google' AND email IS NULL AND created_at IS NOT NULL`); n != 1 {
				t.Fatalf("connected account lost or email not nullable: %d", n)
			}

			// The hash is unique across kinds.
			dup := `INSERT INTO access_tokens (id, user_id, kind, token_hash, scope) VALUES ('00000000-0000-7000-8000-000000000009', ?, 'session', ?, 'full')`
			if _, err := db.ExecContext(ctx, rebind(d, dup), user, hash); err == nil {
				t.Fatal("duplicate token_hash accepted")
			}

			// The OAuth tables exist and cascade from clients and users.
			const client = "00000000-0000-7000-8000-000000000010"
			mustExec(t, db, d, `INSERT INTO oauth_clients (id, name, redirect_uris, created_at) VALUES (?, 'c', '[]', ?)`, client, now)
			mustExec(t, db, d, `INSERT INTO oauth_grants (id, user_id, client_id, refresh_token_hash, created_at, last_used_at, expires_at)
				VALUES ('00000000-0000-7000-8000-000000000011', ?, ?, 'r', ?, ?, ?)`, user, client, now, now, exp)
			mustExec(t, db, d, `INSERT INTO oauth_refresh_tokens_spent (token_hash, grant_id, spent_at) VALUES ('old', '00000000-0000-7000-8000-000000000011', ?)`, now)
			mustExec(t, db, d, `DELETE FROM oauth_clients WHERE id = ?`, client)
			if n := count(t, db, d, `SELECT count(*) FROM oauth_refresh_tokens_spent`); n != 0 {
				t.Fatalf("spent refresh hashes survived their client: %d", n)
			}
		})
	}
}
