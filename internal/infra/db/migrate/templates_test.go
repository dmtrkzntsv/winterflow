package migrate

import "testing"

// 0016_templates_x_winterflow drops the old templates and their instances
// (nothing is migrated), clears the template_upgrade rollouts but keeps the
// reloads, and the new tables hold org and catalog templates apart.
func TestTemplatesXWinterflowMigration(t *testing.T) {
	const (
		org    = "00000000-0000-7000-8000-000000000001"
		server = "00000000-0000-7000-8000-000000000011"
		tpl    = "00000000-0000-7000-8000-000000000021"
		cat    = "00000000-0000-7000-8000-000000000022"
		now    = "2026-10-14 12:00:00"
	)
	for _, d := range engines(t) {
		t.Run(d, func(t *testing.T) {
			ctx := tctx(t)
			db := openEmpty(t, d)
			if err := MigrateTo(ctx, db, d, 15); err != nil {
				t.Fatal(err)
			}
			mustExec(t, db, d, `INSERT INTO organizations (organization_id, name, created_at) VALUES (?, 'o', ?)`, org, now)
			mustExec(t, db, d, `INSERT INTO servers (server_id, organization_id, name, created_at) VALUES (?, ?, 'box', ?)`, server, org, now)
			mustExec(t, db, d, `INSERT INTO templates (id, org_id, name, latest_revision, created_at, updated_at) VALUES (?, ?, 'shop', 1, ?, ?)`, tpl, org, now, now)
			mustExec(t, db, d, `INSERT INTO template_revisions (template_id, revision, body, created_at) VALUES (?, 1, '{}', ?)`, tpl, now)
			mustExec(t, db, d, `INSERT INTO template_instances (id, org_id, instance_key, app_id, server_id, template_id, revision, params, secret_names, request_hash, state, created_at, updated_at)
				VALUES (?, ?, 'k', ?, ?, ?, 1, '{}', '[]', 'h', 'created', ?, ?)`, "00000000-0000-7000-8000-000000000031", org, "00000000-0000-7000-8000-000000000041", server, tpl, now, now)
			for id, kind := range map[string]string{
				"00000000-0000-7000-8000-000000000050": "template_upgrade",
				"00000000-0000-7000-8000-000000000051": "vars_reload",
			} {
				mustExec(t, db, d, `INSERT INTO rollouts (id, org_id, kind, scope, scope_key, state, created_at) VALUES (?, ?, ?, '{}', ?, 'done', ?)`,
					id, org, kind, tpl, now)
			}

			if err := Migrate(ctx, db, d); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"templates", "template_revisions", "template_instances"} {
				if n := count(t, db, d, `SELECT count(*) FROM `+table); n != 0 {
					t.Fatalf("%s kept %d rows", table, n)
				}
			}
			if n := count(t, db, d, `SELECT count(*) FROM rollouts WHERE kind = 'template_upgrade'`); n != 0 {
				t.Fatalf("%d upgrade rollouts kept", n)
			}
			if n := count(t, db, d, `SELECT count(*) FROM rollouts WHERE kind = 'vars_reload'`); n != 1 {
				t.Fatalf("%d reload rollouts kept, want 1", n)
			}

			// An org template needs its org; a catalog one a slug and no org.
			mustExec(t, db, d, `INSERT INTO templates (id, source, org_id, name, latest_revision, created_at, updated_at) VALUES (?, 'org', ?, 'shop', 1, ?, ?)`, tpl, org, now, now)
			mustExec(t, db, d, `INSERT INTO templates (id, source, slug, name, latest_revision, created_at, updated_at) VALUES (?, 'catalog', 'shop', 'Shop', 1, ?, ?)`, cat, now, now)
			for _, bad := range []string{
				`INSERT INTO templates (id, source, name, latest_revision, created_at, updated_at) VALUES ('00000000-0000-7000-8000-000000000023', 'org', 'x', 1, '` + now + `', '` + now + `')`,
				`INSERT INTO templates (id, source, org_id, slug, name, latest_revision, created_at, updated_at) VALUES ('00000000-0000-7000-8000-000000000024', 'catalog', '` + org + `', 's', 'x', 1, '` + now + `', '` + now + `')`,
				`INSERT INTO templates (id, source, name, latest_revision, created_at, updated_at) VALUES ('00000000-0000-7000-8000-000000000025', 'catalog', 'x', 1, '` + now + `', '` + now + `')`,
				`INSERT INTO templates (id, source, slug, name, latest_revision, created_at, updated_at) VALUES ('00000000-0000-7000-8000-000000000026', 'catalog', 'shop', 'y', 1, '` + now + `', '` + now + `')`,
			} {
				if _, err := db.ExecContext(ctx, bad); err == nil {
					t.Fatalf("accepted: %s", bad)
				}
			}
			// A per-org upgrade scope key fits.
			mustExec(t, db, d, `INSERT INTO rollouts (id, org_id, kind, scope, scope_key, state, created_at) VALUES ('00000000-0000-7000-8000-000000000060', ?, 'template_upgrade', '{}', ?, 'queued', ?)`,
				org, org+":"+cat, now)
			if got := count(t, db, d, `SELECT length(scope_key) FROM rollouts WHERE kind = 'template_upgrade'`); got != len(org+":"+cat) {
				t.Fatalf("scope key length %d", got)
			}
		})
	}
}
