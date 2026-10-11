package main

import "testing"

func TestLanguage(t *testing.T) {
	cases := map[string]string{
		"internal/app/web/routes.go":                         LangGo,
		"web/embed.go":                                       LangGo,
		"web/src/pages/settings-volumes.tsx":                 LangWeb,
		"web/src/lib/volumes.ts":                             LangWeb,
		"web/vite.config.ts":                                 "", // outside the coverage include
		"web/src/vite-env.d.ts":                              "",
		"README.md":                                          "",
		"web/src/index.css":                                  "",
		".github/workflows/ci.yml":                           "",
		"internal/app/web/routes_test.go":                    "",
		"web/src/lib/volumes.test.ts":                        "",
		"web/src/pages/settings-volumes.test.tsx":            "",
		"internal/infra/transport/grpc/proto/hub.pb.go":      "",
		"internal/infra/transport/grpc/proto/hub_grpc.pb.go": "",
		"internal/infra/db/sqlc/gen/sqlite/users.sql.go":     "",
		"internal/infra/db/sqlc/gen/pgsql/querier.go":        "",
		"internal/infra/db/sqlc/gen.go":                      LangGo,
		"web/src/components/ui/button.tsx":                   "",
		"web/src/components/app-status-badge.tsx":            LangWeb,
		"cmd/standalone/main.go":                             "",
		"cmd/standalone/serve.go":                            LangGo,
		"tools/diffcov/main.go":                              "",
		"web/src/main.tsx":                                   "",
		"web/src/test/setup.ts":                              "", // Vitest setup, never in coverage
		"web/src/test/helpers/render.tsx":                    "",
		"src/lib/volumes.ts":                                 "", // TS outside web/
	}
	for p, want := range cases {
		if got := Language(p); got != want {
			t.Errorf("Language(%q) = %q, want %q", p, got, want)
		}
	}
}
