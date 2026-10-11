# PR 4 — Remove Roles (Every User Is an Admin) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Any authenticated member of an organization can do everything any other member can: the owner/admin/member split, its middleware, its route, its rules, its API fields and its UI gating are removed, while authentication, server ownership, the auth rate limiter and the self-removal refusal stay.

**Architecture:** The role gate lives in one place on the server, the `api.Guards.Admin` middleware (`internal/app/web/middleware/rbac`) that handler route tables attach to 14 routes. Deleting the field and the package removes the gate everywhere; the typed route table then regenerates `api/openapi.yaml` (403 disappears where it only came from the gate) and the routes golden (the `rbac` closure leaves 14 middleware chains). Role *management* (`org/update-member`, the last-owner rule, `RoleOf`, the `role` fields of the profile, members and create-user payloads) goes next, layer by layer: route → handler → port → repository → model. The `organization_users.role` column stays and is still written, but nothing reads it. On the web side `isAdmin` leaves the profile context and every consumer (nav menu, volumes card, org members page), and the members page loses its role column, role picker and `update-member` call.

**Tech Stack:** Go 1.25 (module `winterflow`), chi v5, bun (SQLite + Postgres), kin-openapi (contract suite), React 19 + Vite + Vitest 5 (`@testing-library/react`, `user-event`, jsdom), pnpm.

**Spec:** `docs/superpowers/specs/2026-10-10-remove-roles-design.md`.

**Branch:** `feat/remove-roles` (already created, stacked on `ci/contract-suite` = PR #7). The local diff gate for this stacked branch is `make diffcov DIFFCOV_BASE=ci/contract-suite`.

**Verified at plan time:** every edit below was applied, task by task, to a scratch copy of `feat/remove-roles` at `82fe67b`. After each task: `go build ./...`, `go vet ./...`, `gofmt -l` clean and `go test ./internal/app/web/... ./internal/infra/db/repository/` green; after the web tasks `vitest run` (24 tests), `tsc -b`, `eslint .` and `vite build` green. Final state: `WF_CONTRACT_GUARD=require go test -race -covermode=atomic -coverpkg=./... ./...` green; `make test-pg` (contract suite on `postgres:16`) green with the regenerated `parity.golden.json`; `tools/diffcov` against the base: **Go 43/43 changed lines (100%), web 5/5 (100%)**; ratchet: Go 74.0% (baseline 74.0, pass), web 12.6% (baseline 5.4, pass, asks for a bump — Task 6 bumps it).

**Resolved spec ambiguities (decided here):**
- **`org/update-member` only changes a role** (`UpdateMemberRequest{UserID, Role}`, nothing else), so the route, `UpdateMember` handler, `UpdateMemberRequest`, `OrgStore.UpdateMemberRole`, `port.UserRepository.UpdateMemberRole`, `DbUserRepository.UpdateMemberRole` and the UI role picker are all removed.
- **Last-owner rule removed.** `model.ErrLastOwner`, `ownerCount` and `memberRole` go. `RemoveMember` keeps one check — the user must be a member of the caller's org, else `model.ErrorUserNotFound` — because it is now the only thing scoping a removal to the caller's org. Self-removal stays refused in the handler (400), so an org always keeps at least the member who removed the others.
- **Role leaves every API payload:** `user.Profile.Role`, `model.Member.Role`, `org.CreateUserRequest.Role`, `org.CreateUserResponse.Role`; `RoleOf` is deleted from `user.ProfileStore`, `port.UserRepository` and the repository. The frontend does not need it for display. `webutil.DecodeBody` ignores unknown fields, so a client still sending `"role"` keeps working.
- **What the DB gets:** the `role` column stays (no migration). Org creators keep `owner` (`CreateUser`, `BootstrapLocalAdmin`, `RegisterLocalUser` are unchanged). Accounts created inside an org (`CreateMemberUser`) now store `admin` explicitly instead of a caller-chosen role — not the column default `member` — so an older role-aware binary run against the same DB does not demote anyone created after the upgrade. `model.OrganizationRole` keeps only `RoleOwner` and `RoleAdmin` (write-only); `RoleMember` and `IsAdmin` are deleted.
- **`api.Guards` keeps only `AuthLimit`.** Handler `Routes(api.Guards)` signatures are unchanged.
- **403 stays documented where ownership produces it** (docker family, `server/get-public-key`, app routes) and disappears from the six admin `org/*` routes and `server/register`, which had no other 403 source. `get-organization` already documented only 400.
- **Web:** the server registration dialog (`web/src/components/server-creation-dialog.tsx`) had no role gating — unchanged. The nav "Members" item, the org "Edit" button and the Volumes tab's create/delete are now shown to everyone. `register.tsx` copy ("Ask an administrator…", "Create the admin account") stays true and is unchanged.
- **Docs:** `CLAUDE.md` (route-table sentence + Auth paragraph) and `MIGRATION.md` (its "post-migration" current-state paragraphs) are updated. `AGENTS.md` has no role text (`grep -n -i 'admin\|role\|rbac' AGENTS.md` finds nothing role-related) and is unchanged. `SessionUser.role` in `api/openapi.yaml` is go-pkgz's `token.User`, not ours, and stays.
- **Usecases:** `internal/domain/usecase` has no role logic (`grep -rn -i role internal/domain/usecase` is empty); nothing to change there.

## Global Constraints

- Keep fail-closed authorization: authentication (`patauth` + JWT), org ownership of servers (`webutil.RequireServerAccess`; the foreign-org 403 matrix in `internal/app/web/contract/matrix_test.go` stays unchanged), the per-IP auth rate limiter (`api.Guards.AuthLimit`), and the self-removal refusal in `org/remove-member`.
- The `organization_users.role` column stays; no migration is added or changed.
- Never touch `web/src/components/ui/**` (vendored shadcn/ui). Importing from it in tests is fine.
- `api/openapi.yaml`, `internal/app/web/testdata/routes.golden` and `internal/app/web/contract/testdata/parity.golden.json` are regenerated deliberately (`make openapi`, `-run TestRoutesGolden -update`, `-run TestParity -update`) and their diffs reviewed against the expectations stated in each task.
- No new Go modules or npm packages; no env var changes (so `.env.example` / `web/.env.example` stay unchanged).
- `gofmt -l $(git ls-files '*.go')` prints nothing; `go vet ./...` passes; `pnpm --dir web run lint` and `pnpm --dir web run build` pass.
- CI gates: ≥80% of changed executable Go lines and ≥80% of changed web lines covered (`tools/diffcov`), totals not below `coverage-baseline.json` by more than 0.1.
- Commits: Conventional Commits, imperative mood. Each message ends with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.

## Review Focus

1. **Accounts created before the upgrade.** Their stored role is still `member`; after the upgrade they must be able to do everything (create a network, update the agent…), because nothing may read the column any more. Pinned in Task 3: the docker contract subtest overwrites the member's stored role with `member` (`env.setStoredRole`) before running every docker route.
2. **A browser tab opened before the upgrade** still posts `{"name","email","role"}` to `org/create-user`, with any role value (`member`, `admin`, `owner`, garbage). Expected: the account is created, the role ignored. Pinned in Task 3 by `TestCreateUserIgnoresStaleRole`.
3. **`org/remove-member` with a user id from another org.** With the role checks gone, the repository's membership check is the only thing scoping the delete; it must refuse (400) and leave the other org's account intact. Pinned in Task 2 by `TestRemoveMemberAnyMemberOnlyInOrg` (repository) and `TestRemoveMember` (handler).
4. **A member removes the org's creator.** No owner rule any more: it succeeds, the org keeps working with the remaining member, and the creator's password stops working; removing yourself is still 400 for everyone. Pinned in Task 2 (contract `a member removes the org's creator`, repository test) and Task 1 (contract `members administer` refuses a member's self-removal).
5. **A member and another org's server.** Dropping the role gate must not open foreign servers: a member mutating a server of another org still gets 403 and nothing reaches the agent. Pinned in Task 1 in the docker contract subtest (`TestForeignServer` only probes with the owner).

---

## File Map

**Delete**
- `internal/app/web/middleware/rbac/rbac.go`, `internal/app/web/middleware/rbac/rbac_test.go` — the role middleware.

**Go, production**
- `internal/app/web/api/route.go` — `Guards` loses `Admin`.
- `internal/app/web/routes.go` — no `rbac` wiring.
- `internal/app/web/api/openapi/openapi.go` — `Error` doc comment.
- `internal/app/web/handler/docker/routes.go`, `handler/server/routes.go`, `handler/org/routes.go` — no admin middleware; 403 only where ownership produces it; `update-member` route gone.
- `internal/app/web/handler/org/handler.go` — no `UpdateMember`, no role in create-user, no last-owner branch.
- `internal/app/web/handler/user/handler.go`, `handler/user/routes.go` — profile without role.
- `internal/domain/port/user.go` — `CreateMemberUser` without role; no `UpdateMemberRole`, `RoleOf`.
- `internal/domain/model/organization.go`, `internal/domain/model/user.go` — write-only role constants; no `ErrLastOwner`; `Member` without role.
- `internal/infra/db/repository/user.go` — `CreateMemberUser` stores `admin`; `RemoveMember` membership-only; no `UpdateMemberRole`, `ownerCount`, `memberRole`, `RoleOf`.

**Go, tests and generated**
- `internal/app/web/handler/{app,docker,org,server,user}/routes_test.go`, `handler/org/handler_test.go`, `handler/user/handler_test.go`, `internal/infra/db/repository/user_test.go`.
- `internal/app/web/contract/{docker,org,server,user,matrix,client,harness}_test.go`, `contract/testdata/parity.golden.json`.
- `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`.

**Web**
- `web/src/pages/org-members.tsx` + new `web/src/pages/org-members.test.tsx`.
- `web/src/context/profile-context-base.ts`, `web/src/context/profile-context.tsx` + new `web/src/context/profile-context.test.tsx`.
- `web/src/components/nav-user.tsx` + new `web/src/components/nav-user.test.tsx`.
- `web/src/pages/settings-volumes.tsx`, `web/src/pages/settings-volumes.test.tsx`.

**Docs / CI**
- `CLAUDE.md`, `MIGRATION.md`, `coverage-baseline.json`.

---

### Task 1: Drop the role gate from the route table

Every route that carried `g.Admin` becomes callable by any org member. Role *data* is untouched in this task (create-user still takes a role, update-member still exists) — only the gate goes.

**Files:**
- Delete: `internal/app/web/middleware/rbac/rbac.go`, `internal/app/web/middleware/rbac/rbac_test.go`
- Modify: `internal/app/web/api/route.go` (the `Guards` type), `internal/app/web/routes.go` (imports; the `guards :=` block), `internal/app/web/api/openapi/openapi.go:63-64` (comment), `internal/app/web/handler/docker/routes.go` (whole file), `internal/app/web/handler/server/routes.go` (doc comment, signature, `server/register` entry), `internal/app/web/handler/org/routes.go` (whole file)
- Test: `internal/app/web/handler/{docker,server,org}/routes_test.go` (rewrite), `handler/app/routes_test.go`, `handler/user/routes_test.go`, `internal/app/web/contract/{docker,org,server}_test.go`
- Regenerate: `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`

**Interfaces:**
- Consumes: nothing new.
- Produces: `api.Guards` is `struct{ AuthLimit Middleware }` (no `Admin`). `docker`, `server` and `org` `Routes(api.Guards)` return routes with empty `Middleware`. Org routes document `Errors: []int{http.StatusBadRequest}`; `POST /api/v1/server/register` documents `[]int{http.StatusBadRequest}`; docker routes keep `[]int{http.StatusBadRequest, http.StatusForbidden}`.

- [ ] **Step 1: Write the failing route-table tests**

Replace `internal/app/web/handler/docker/routes_test.go` with:

```go
package docker

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"winterflow/internal/app/web/api"
)

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	want := []string{
		"GET /api/v1/registry/list",
		"POST /api/v1/registry/create",
		"POST /api/v1/registry/delete",
		"GET /api/v1/network/list",
		"POST /api/v1/network/create",
		"POST /api/v1/network/delete",
		"GET /api/v1/volume/list",
		"POST /api/v1/volume/create",
		"POST /api/v1/volume/delete",
		"POST /api/v1/agent/update",
	}
	var got []string
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		got = append(got, key)
		if rt.Handler == nil || rt.Auth != api.User || !rt.Accepted || rt.Result == nil {
			t.Errorf("%s: want a User, Accepted route with a handler and a Result", key)
		}
		if _, ok := rt.Query.(api.ServerQuery); !ok {
			t.Errorf("%s: Query = %T, want api.ServerQuery", key, rt.Query)
		}
		// No role gate: every org member may call every docker route.
		if len(rt.Middleware) != 0 {
			t.Errorf("%s: %d middleware, want none", key, len(rt.Middleware))
		}
		// 403 stays documented: the server may belong to another org.
		if !slices.Contains(rt.Errors, http.StatusForbidden) {
			t.Errorf("%s: Errors = %v, want 403 for a foreign server", key, rt.Errors)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v\nwant %v", got, want)
	}
}
```

Replace `internal/app/web/handler/server/routes_test.go` with:

```go
package server

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"winterflow/internal/app/web/api"
)

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	// "METHOD path" -> documents 403 (server_id of another org).
	want := map[string]bool{
		"GET /api/v1/server/get-servers":        false,
		"GET /api/v1/server/get-servers-status": false,
		"GET /api/v1/server/get-public-key":     true,
		"POST /api/v1/server/register":          false,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted {
			t.Errorf("%s: want a synchronous User route with a handler", key)
		}
		// No role gate: any org member may register a server.
		if len(rt.Middleware) != 0 {
			t.Errorf("%s: %d middleware, want none", key, len(rt.Middleware))
		}
		got[key] = slices.Contains(rt.Errors, http.StatusForbidden)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes (path -> 403) = %v\nwant %v", got, want)
	}
}
```

Replace `internal/app/web/handler/org/routes_test.go` with (update-member is still listed; Task 2 removes it):

```go
package org

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"winterflow/internal/app/web/api"
)

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	want := []string{
		"POST /api/v1/org/create-user",
		"GET /api/v1/org/get-members",
		"POST /api/v1/org/update-member",
		"POST /api/v1/org/remove-member",
		"POST /api/v1/org/reset-member-password",
		"GET /api/v1/org/get-organization",
		"POST /api/v1/org/update-organization",
	}
	var got []string
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		got = append(got, key)
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted {
			t.Errorf("%s: want a synchronous User route with a handler", key)
		}
		// No role gate, so nothing answers 403: every member administers.
		if len(rt.Middleware) != 0 || slices.Contains(rt.Errors, http.StatusForbidden) {
			t.Errorf("%s: middleware %d, errors %v; want none and no 403", key, len(rt.Middleware), rt.Errors)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v\nwant %v", got, want)
	}
}
```

In `internal/app/web/handler/app/routes_test.go`: delete the `"net/http"` import and the line `func adminGuard(next http.Handler) http.Handler { return next }`; change `Routes(api.Guards{Admin: adminGuard})` to `Routes(api.Guards{})`; change the comment `// "METHOD path" -> runs GetAppsValidationMiddleware. No app route is` / `// admin-only: members keep the full app lifecycle.` to `// "METHOD path" -> runs GetAppsValidationMiddleware.`; and replace the middleware loop body

```go
		for _, mw := range rt.Middleware {
			p := reflect.ValueOf(mw).Pointer()
			if p == reflect.ValueOf(adminGuard).Pointer() {
				t.Errorf("%s: app routes must not be admin-only", key)
			}
			validated = validated || p == validate
		}
```

with

```go
		for _, mw := range rt.Middleware {
			validated = validated || reflect.ValueOf(mw).Pointer() == validate
		}
```

In `internal/app/web/handler/user/routes_test.go`: delete the `"net/http"` import and the `adminGuard` func; change `Routes(api.Guards{Admin: adminGuard})` to `Routes(api.Guards{})`.

- [ ] **Step 2: Write the failing contract tests**

In `internal/app/web/contract/docker_test.go`, delete the subtest

```go
	t.Run("members cannot mutate infrastructure", func(t *testing.T) {
		m := owner.member(t)
		m.do(t, call{Method: http.MethodPost, Path: "/api/v1/network/create", Query: q,
			Body: command.CreateNetworkRequest{Name: "x"}}).expect(t, http.StatusForbidden)
		m.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/volume/list", Query: q}, nil)
	})
```

and insert this one **between** the owner's `for _, tc := range cases` loop and `t.Run("agent errors", …)` (it must run before `agent.fail` scripts failures that stay in force for the rest of the test):

```go
	// No roles: a member runs every docker route the owner does.
	t.Run("members mutate infrastructure", func(t *testing.T) {
		m := owner.member(t)
		for _, tc := range cases {
			m.succeed(t, call{Method: tc.method, Path: tc.path, Query: q, Body: tc.body}, nil)
		}
		// Org ownership still holds: another org's server stays 403.
		n := e.agent.count()
		m.do(t, call{Method: http.MethodPost, Path: "/api/v1/network/create", Query: sq(e.foreignServer(t)),
			Body: command.CreateNetworkRequest{Name: "x"}}).expect(t, http.StatusForbidden)
		if e.agent.count() != n {
			t.Fatal("a command for a foreign server reached the agent")
		}
	})
```

In `internal/app/web/contract/org_test.go`, replace

```go
	t.Run("members cannot administer", func(t *testing.T) {
		m.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusForbidden)
		m.get(t, "/api/v1/org/get-organization", nil).expect(t, http.StatusOK)
	})
```

with

```go
	// No roles: a member administers the org exactly like its creator.
	t.Run("members administer", func(t *testing.T) {
		m.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusOK)
		m.post(t, "/api/v1/org/update-organization", org.UpdateOrganizationRequest{Name: "Acme 2"}).
			expect(t, http.StatusOK)
		var created org.CreateUserResponse
		m.post(t, "/api/v1/org/create-user", org.CreateUserRequest{Name: "Carol", Email: "carol@example.test", Role: "member"}).
			expect(t, http.StatusOK).data(t, &created)
		m.post(t, "/api/v1/org/reset-member-password", org.MemberRequest{UserID: created.UserID}).
			expect(t, http.StatusOK)
		m.post(t, "/api/v1/org/remove-member", org.MemberRequest{UserID: created.UserID}).
			expect(t, http.StatusOK)
		m.post(t, "/api/v1/org/remove-member", org.MemberRequest{UserID: m.userID}).
			expect(t, http.StatusBadRequest)
	})
```

In `internal/app/web/contract/server_test.go`, append inside `TestServerRoutes`, after the `register claims a pending server by code` subtest:

```go

	// No roles: a member claims a server into the shared org, and the
	// owner sees it.
	t.Run("a member registers a server", func(t *testing.T) {
		m := owner.member(t)
		var reg server.RegisterServerResponse
		m.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: e.seedPendingServer(t)}).
			expect(t, http.StatusOK).data(t, &reg)
		var list server.GetServersResponse
		owner.get(t, "/api/v1/server/get-servers", nil).expect(t, http.StatusOK).data(t, &list)
		found := false
		for _, s := range list.Servers {
			found = found || s.ID == reg.Server.ID
		}
		if !found {
			t.Fatalf("owner's servers %+v lack the member's %s", list.Servers, reg.Server.ID)
		}
	})
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/app/web/handler/... ./internal/app/web/contract/`
Expected: FAIL — `handler/docker`, `handler/server`, `handler/org`: `…: 1 middleware, want none` (the zero `Guards` puts a nil `g.Admin` in `Middleware`), org also `errors [400 403]`; contract: `TestDockerRoutes/members_mutate_infrastructure`, `TestOrgRoutes/members_administer`, `TestServerRoutes/a_member_registers_a_server` with `status = 403, want 202/200`. `handler/app` and `handler/user` pass.

- [ ] **Step 4: Remove the gate**

Delete the rbac package:

```bash
git rm -r internal/app/web/middleware/rbac
```

In `internal/app/web/api/route.go`, replace the `Guards` declaration (comment + struct) with:

```go
// Guards are the shared middlewares routes.go builds from live dependencies
// and hands to every handler table. Handler tables put them in
// Route.Middleware per route. The zero value (all nil) is only for spec
// generation: Register refuses a nil middleware.
//
// There is no role guard: every member of an organization may call every
// route. Authorization is authentication plus org ownership of the
// addressed server (webutil.RequireServerAccess, inside the handlers).
type Guards struct {
	// AuthLimit is the per-IP limiter for bcrypt-priced endpoints.
	AuthLimit Middleware
}
```

In `internal/app/web/routes.go`, delete the import line `"winterflow/internal/app/web/middleware/rbac"` and replace

```go
	guards := api.Guards{
		// Admin gates administration (member management, server
		// registration, infrastructure mutations) to owners/admins. Members
		// keep the full app lifecycle.
		Admin:     rbac.RequireAdmin(s.Deps.UserService),
		AuthLimit: authLimiter.Middleware,
	}
```

with

```go
	// No role guard: every org member may do everything; server-addressed
	// handlers still check the caller's org owns the server.
	guards := api.Guards{AuthLimit: authLimiter.Middleware}
```

In `internal/app/web/api/openapi/openapi.go`, change the comment line `// util.Unauthorized (401), util.Forbidden and rbac.RequireAdmin (403).` to `// util.Unauthorized (401) and util.Forbidden (403, a server of another org).`

Replace `internal/app/web/handler/docker/routes.go` with:

```go
package docker

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/command"
)

// Routes is the docker handler's slice of the route table. Every route is
// server-addressed (server_id query) and agent-bound (202 + request_id).
// Any org member may call any of them; 403 means the server belongs to
// another org.
func (h *Handler) Routes(api.Guards) []api.Route {
	errs := []int{http.StatusBadRequest, http.StatusForbidden}
	route := func(method, path string, hf http.HandlerFunc, summary string, req, result any) api.Route {
		return api.Route{
			Method: method, Path: path, Handler: hf, Auth: api.User,
			Summary: summary, Tag: "docker", Query: api.ServerQuery{}, Request: req,
			Accepted: true, Result: result, Errors: errs,
		}
	}
	return []api.Route{
		route(http.MethodGet, "/api/v1/registry/list", h.ListRegistries,
			"List the agent's Docker registry logins", nil, command.ListRegistriesResponse{}),
		route(http.MethodPost, "/api/v1/registry/create", h.CreateRegistry,
			"Log the agent in to a registry", command.CreateRegistryRequest{}, command.CreateRegistryResponse{}),
		route(http.MethodPost, "/api/v1/registry/delete", h.DeleteRegistry,
			"Log the agent out of a registry", command.DeleteRegistryRequest{}, command.DeleteRegistryResponse{}),
		route(http.MethodGet, "/api/v1/network/list", h.ListNetworks,
			"List Docker networks", nil, command.ListNetworksResponse{}),
		route(http.MethodPost, "/api/v1/network/create", h.CreateNetwork,
			"Create a Docker network", command.CreateNetworkRequest{}, command.CreateNetworkResponse{}),
		route(http.MethodPost, "/api/v1/network/delete", h.DeleteNetwork,
			"Delete a Docker network", command.DeleteNetworkRequest{}, command.DeleteNetworkResponse{}),
		route(http.MethodGet, "/api/v1/volume/list", h.ListVolumes,
			"List Docker volumes", nil, command.ListVolumesResponse{}),
		route(http.MethodPost, "/api/v1/volume/create", h.CreateVolume,
			"Create a Docker volume", command.CreateVolumeRequest{}, command.CreateVolumeResponse{}),
		route(http.MethodPost, "/api/v1/volume/delete", h.DeleteVolume,
			"Delete a Docker volume", command.DeleteVolumeRequest{}, command.DeleteVolumeResponse{}),
		route(http.MethodPost, "/api/v1/agent/update", h.UpdateAgent,
			"Self-update the agent binary", command.UpdateAgentRequest{}, command.UpdateAgentResponse{}),
	}
}
```

In `internal/app/web/handler/server/routes.go`, replace

```go
// Routes is the server handler's slice of the route table. Registering a
// server is admin-only via g.Admin.
func (h *Handler) Routes(g api.Guards) []api.Route {
```

with

```go
// Routes is the server handler's slice of the route table. Any org member
// may register a server; it joins the caller's org.
func (h *Handler) Routes(api.Guards) []api.Route {
```

and replace the `server/register` entry's body

```go
			Method: http.MethodPost, Path: "/api/v1/server/register", Handler: h.Register, Auth: api.User,
			Middleware: []api.Middleware{g.Admin},
			Summary:    "Claim a pending server by its pairing code", Tag: "server",
			Request: RegisterServerRequest{}, Response: RegisterServerResponse{},
			Errors: []int{http.StatusBadRequest, http.StatusForbidden},
```

with

```go
			Method: http.MethodPost, Path: "/api/v1/server/register", Handler: h.Register, Auth: api.User,
			Summary: "Claim a pending server by its pairing code", Tag: "server",
			Request: RegisterServerRequest{}, Response: RegisterServerResponse{},
			Errors: []int{http.StatusBadRequest},
```

Replace `internal/app/web/handler/org/routes.go` with:

```go
package org

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

// Routes is the org handler's slice of the route table. Every member of the
// org may call every route; each one acts on the caller's own org only.
func (h *Handler) Routes(api.Guards) []api.Route {
	bad := []int{http.StatusBadRequest}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/org/create-user", Handler: h.CreateUser, Auth: api.User,
			Summary: "Create an account in the caller's org (temp password)", Tag: "org",
			Request: CreateUserRequest{}, Response: CreateUserResponse{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/org/get-members", Handler: h.GetMembers, Auth: api.User,
			Summary: "List the caller's org members", Tag: "org",
			Response: []model.Member{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/update-member", Handler: h.UpdateMember, Auth: api.User,
			Summary: "Change a member's role", Tag: "org",
			Request: UpdateMemberRequest{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/remove-member", Handler: h.RemoveMember, Auth: api.User,
			Summary: "Remove a member and their account", Tag: "org",
			Request: MemberRequest{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/reset-member-password", Handler: h.ResetMemberPassword, Auth: api.User,
			Summary: "Issue a member a new temp password", Tag: "org",
			Request: MemberRequest{}, Response: ResetMemberPasswordResponse{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/org/get-organization", Handler: h.GetOrganization, Auth: api.User,
			Summary: "The caller's org name, icon and color", Tag: "org",
			Response: model.Organization{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/update-organization", Handler: h.UpdateOrganization, Auth: api.User,
			Summary: "Rename or restyle the caller's org", Tag: "org",
			Request: UpdateOrganizationRequest{}, Errors: bad,
		},
	}
}
```

- [ ] **Step 5: Regenerate the route golden and the OpenAPI document**

```bash
go build ./...
go test ./internal/app/web/ -run TestRoutesGolden -update
make openapi
git diff --stat -- internal/app/web/testdata/routes.golden api/openapi.yaml
```

Expected: `routes.golden` changes 14 lines, each only losing the trailing `, closure@rbac/rbac.go:23` (agent/update, network create/delete, registry create/delete, volume create/delete, server/register, and the six admin `org/*` routes); `grep -c rbac internal/app/web/testdata/routes.golden` prints `0`. `api/openapi.yaml` loses exactly 42 lines: the 6-line `"403"` response block on 7 operations (the six admin `org/*` routes and `server/register`); every docker route keeps its 403.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go vet ./internal/app/web/... && go test ./internal/app/web/...`
Expected: PASS (all packages `ok`; `TestRoutesGolden` and `TestOpenAPIUpToDate` included).

- [ ] **Step 7: Commit**

```bash
git add -A internal/app/web api/openapi.yaml
git commit -m "refactor(api)!: drop the admin role gate

Every org member may now call the formerly admin-only routes (registry,
network and volume mutations, agent update, server registration, org
management). Server ownership and the auth rate limiter stay.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Remove `org/update-member` and the last-owner rule

`update-member` only changed a role (`UpdateMemberRequest{UserID, Role}`), so it goes entirely. With no owner rule, `RemoveMember` keeps only the org-membership check.

**Files:**
- Modify: `internal/app/web/handler/org/handler.go` (package doc, `OrgStore`, delete `UpdateMemberRequest`/`UpdateMember`, `RemoveMember`, two doc comments), `internal/app/web/handler/org/routes.go` (delete the update-member entry), `internal/domain/port/user.go` (`UpdateMemberRole`, `RemoveMember` doc), `internal/domain/model/user.go` (`ErrLastOwner`), `internal/infra/db/repository/user.go` (delete `ownerCount`, `memberRole`, `UpdateMemberRole`; rewrite `RemoveMember`)
- Test: `internal/app/web/handler/org/handler_test.go`, `internal/app/web/handler/org/routes_test.go`, `internal/infra/db/repository/user_test.go`, `internal/app/web/contract/{org,matrix}_test.go`
- Regenerate: `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`

**Interfaces:**
- Consumes: Task 1's org route table.
- Produces: `org.OrgStore` without `UpdateMemberRole`; `port.UserRepository` without `UpdateMemberRole`; `(*DbUserRepository).RemoveMember(ctx, orgID, userID string) error` returns `model.ErrorUserNotFound` when `userID` is not a member of `orgID`, otherwise deletes the user row. `model.ErrLastOwner` no longer exists.

- [ ] **Step 1: Write the failing tests**

In `internal/app/web/handler/org/routes_test.go`, delete the line `"POST /api/v1/org/update-member",` from `want`.

In `internal/app/web/handler/org/handler_test.go`:
- In `fakeOrgStore`, delete the fields `updated    map[string]string` and `lastOwner  string`.
- Replace the `UpdateMemberRole` and `RemoveMember` fake methods with:

```go
func (f *fakeOrgStore) RemoveMember(_ context.Context, _, userID string) error {
	if userID == "not-in-org" {
		return model.ErrorUserNotFound
	}
	f.removed = append(f.removed, userID)
	return nil
}
```

- Replace `TestUpdateMemberLastOwnerGuard` and `TestRemoveMemberSelfForbidden` with:

```go
func TestRemoveMember(t *testing.T) {
	h, f := newHandler()
	// Caller is admin-1; removing self is refused (it would lock them out).
	if w := post(h.RemoveMember, `{"user_id":"admin-1"}`); w.Code != http.StatusBadRequest {
		t.Errorf("self-remove: code = %d, want 400", w.Code)
	}
	if w := post(h.RemoveMember, `{"user_id":""}`); w.Code != http.StatusBadRequest {
		t.Errorf("empty user_id: code = %d, want 400", w.Code)
	}
	// A user outside the caller's org is a 400, not a removal.
	if w := post(h.RemoveMember, `{"user_id":"not-in-org"}`); w.Code != http.StatusBadRequest {
		t.Errorf("foreign user: code = %d, want 400", w.Code)
	}
	// Any other member goes, whoever created the org: there is no owner.
	if w := post(h.RemoveMember, `{"user_id":"m-1"}`); w.Code != http.StatusOK {
		t.Errorf("remove member: code = %d", w.Code)
	}
	if len(f.removed) != 1 || f.removed[0] != "m-1" {
		t.Errorf("removed = %v, want [m-1]", f.removed)
	}
}
```

In `internal/infra/db/repository/user_test.go`, replace `TestUpdateMemberRoleLastOwnerGuard` (whole function) with:

```go
func TestRemoveMemberAnyMemberOnlyInOrg(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	creator, _ := repo.BootstrapLocalAdmin(ctx, "", "boss@z.io", "bootpass-123")
	orgID, _ := repo.PrimaryOrganizationID(ctx, creator.ID)
	m, _ := repo.CreateMemberUser(ctx, orgID, "M", "m@z.io", "member", "temp-password1")
	outsider, err := repo.RegisterLocalUser(ctx, "Eve", "eve@z.io", "evepass-1234")
	if err != nil {
		t.Fatal(err)
	}

	// A user of another org is not a member here: refused, and untouched.
	if err := repo.RemoveMember(ctx, orgID, outsider.ID); !errors.Is(err, model.ErrorUserNotFound) {
		t.Errorf("remove outsider err = %v, want ErrorUserNotFound", err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "eve@z.io", "evepass-1234"); err != nil {
		t.Errorf("outsider lost their account: %v", err)
	}

	// No owner rule: the org's creator can be removed like anyone else, and
	// the org lives on with the remaining member.
	if err := repo.RemoveMember(ctx, orgID, creator.ID); err != nil {
		t.Fatalf("remove creator: %v", err)
	}
	members, err := repo.ListMembers(ctx, orgID)
	if err != nil || len(members) != 1 || members[0].ID != m.ID {
		t.Fatalf("members after removing the creator = %+v, %v", members, err)
	}
	if got, err := repo.PrimaryOrganizationID(ctx, m.ID); err != nil || got != orgID {
		t.Errorf("remaining member's org = %q, %v; want %q", got, err, orgID)
	}
}
```

In `internal/app/web/contract/matrix_test.go`, delete the probe line beginning `"POST /api/v1/org/update-member":` (then `gofmt -w internal/app/web/contract/matrix_test.go`).

In `internal/app/web/contract/org_test.go`, delete

```go
	owner.post(t, "/api/v1/org/update-member", org.UpdateMemberRequest{UserID: m.userID, Role: "admin"}).expect(t, http.StatusOK)
	m.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusOK)
	t.Run("the last owner cannot be demoted", func(t *testing.T) {
		owner.post(t, "/api/v1/org/update-member", org.UpdateMemberRequest{UserID: owner.userID, Role: "member"}).
			expect(t, http.StatusBadRequest)
	})

```

and append at the end of `TestOrgRoutes`, after the `cannot remove yourself` subtest:

```go

	// No owner rule: a member may remove the org's creator, and the org
	// carries on with the member.
	t.Run("a member removes the org's creator", func(t *testing.T) {
		m2 := owner.member(t)
		m2.post(t, "/api/v1/org/remove-member", org.MemberRequest{UserID: owner.userID}).expect(t, http.StatusOK)
		m2.get(t, "/api/v1/org/get-members", nil).expect(t, http.StatusOK).data(t, &members)
		if len(members) != 1 || members[0].ID != m2.userID {
			t.Fatalf("members after removing the creator = %+v", members)
		}
		// The creator's account is gone: their password no longer logs in.
		e.anon().do(t, call{Method: http.MethodPost, Path: "/auth/local/login", Body: map[string]string{
			"user": owner.email, "passwd": ownerPassword, "aud": "contract",
		}}).expect(t, http.StatusForbidden)
	})
```

(`owner.member(t)` reuses `member@example.test`; the first member was removed above, so the email is free again.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/app/web/handler/org/ ./internal/infra/db/repository/ ./internal/app/web/contract/`
Expected: FAIL — `handler/org` does not compile (`*fakeOrgStore does not implement OrgStore (missing method UpdateMemberRole)`); repository: `remove creator: cannot remove the last owner`; contract: `TestOrgRoutes/a_member_removes_the_org's_creator` gets 400 instead of 200, `TestProbesCoverTable` reports `POST /api/v1/org/update-member takes input but has no malformed probe`, and the coverage guard in `TestMain` lists `POST /api/v1/org/update-member` (the route still exists but nothing calls it).

- [ ] **Step 3: Remove the route, handler, port method, error and repository rules**

In `internal/app/web/handler/org/routes.go`, delete the entry:

```go
		{
			Method: http.MethodPost, Path: "/api/v1/org/update-member", Handler: h.UpdateMember, Auth: api.User,
			Summary: "Change a member's role", Tag: "org",
			Request: UpdateMemberRequest{}, Errors: bad,
		},
```

In `internal/app/web/handler/org/handler.go`:
- Replace the package doc with:

```go
// Package org serves organization member management: create accounts with
// temp passwords, list members, reset passwords, remove. There are no roles:
// every member of an org may do all of it. Handlers scope every operation
// to the caller's own organization.
```

- Delete the `OrgStore` line `UpdateMemberRole(ctx context.Context, orgID, userID, role string) error`.
- Delete `UpdateMemberRequest` and `UpdateMember` (from `// UpdateMemberRequest is the body of POST /api/v1/org/update-member.` through the end of `func (h *Handler) UpdateMember`).
- Replace the `RemoveMember` doc comment with:

```go
// RemoveMember deletes a member (and their user account) from the caller's
// org. Self-removal is refused, so an org always keeps the member who
// removed the others.
```

- In `RemoveMember`, replace

```go
	if err := h.users.RemoveMember(r.Context(), orgID, req.UserID); err != nil {
		if err == model.ErrLastOwner {
			webutil.Error(w, "cannot remove the last owner", nil)
			return
		}
		webutil.Error(w, "failed to remove member", nil)
```

with

```go
	if err := h.users.RemoveMember(r.Context(), orgID, req.UserID); err != nil {
		webutil.Error(w, "failed to remove member", nil)
```

- Change `// Visible to every member — only updates are admin-gated.` to `// Visible to every member.` and `// UpdateOrganization sets the org's name/icon/color (admin-only via route).` to `// UpdateOrganization sets the org's name/icon/color.`

In `internal/domain/port/user.go`, replace

```go
	// UpdateMemberRole changes a member's role. model.ErrLastOwner guards
	// demoting the only owner.
	UpdateMemberRole(ctx context.Context, orgID, userID, role string) error
	// RemoveMember deletes the member's user row outright (cascades to
	// credentials, accounts, PATs). model.ErrLastOwner guards the only owner.
```

with

```go
	// RemoveMember deletes the member's user row outright (cascades to
	// credentials, accounts, PATs). model.ErrorUserNotFound when the user is
	// not a member of orgID.
```

In `internal/domain/model/user.go`, delete:

```go
	// ErrLastOwner guards demoting/removing an organization's only owner.
	ErrLastOwner = errors.New("cannot remove the last owner")
```

In `internal/infra/db/repository/user.go`, delete `ownerCount`, `memberRole` and `UpdateMemberRole` (from `// ownerCount reports how many owners the org has` through the end of `func (r *DbUserRepository) UpdateMemberRole`), and replace `RemoveMember` (doc comment + function) with:

```go
// RemoveMember deletes the member's user row outright (memberships,
// credentials, connected accounts, and PATs go with it via FK cascade).
// Only a member of orgID can be removed: anyone else is
// model.ErrorUserNotFound and stays untouched. Safe to delete the user
// itself because an account belongs to exactly one org.
func (r *DbUserRepository) RemoveMember(ctx context.Context, orgID, userID string) error {
	return r.db.Transaction(ctx, func(tx bun.IDB) error {
		member, err := tx.NewSelect().Model((*models.OrganizationUser)(nil)).
			Where("organization_id = ? AND user_id = ?", orgID, userID).
			Exists(ctx)
		if err != nil {
			return err
		}
		if !member {
			return model.ErrorUserNotFound
		}
		_, err = tx.NewDelete().Model((*models.User)(nil)).
			Where("user_id = ?", userID).
			Exec(ctx)
		return err
	})
}
```

- [ ] **Step 4: Regenerate the route golden and the OpenAPI document**

```bash
go build ./... && go vet ./internal/...
go test ./internal/app/web/ -run TestRoutesGolden -update
make openapi
git diff --stat -- internal/app/web/testdata/routes.golden api/openapi.yaml
```

Expected: `routes.golden` loses only the `POST /api/v1/org/update-member` line; `api/openapi.yaml` loses the `/api/v1/org/update-member` path and the `UpdateMemberRequest`/`UpdateMemberRequestInput` component (about 54 lines, no additions).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/app/web/... ./internal/infra/db/repository/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A internal api/openapi.yaml
git commit -m "refactor(org)!: remove update-member and the last-owner rule

update-member only changed a role, so it goes. Any member may remove any
other member of their org, the creator included; removing yourself is
still refused and removal stays scoped to the caller's org.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Stop exposing roles; store `admin` for new accounts

`role` leaves the profile, members and create-user payloads; `RoleOf` goes. New in-org accounts store `admin`.

**Files:**
- Modify: `internal/domain/model/organization.go` (role type + constants), `internal/domain/model/user.go` (`Member`), `internal/domain/port/user.go` (`CreateMemberUser`, `RoleOf`), `internal/infra/db/repository/user.go` (`CreateMemberUser`, `ListMembers`, delete `RoleOf`), `internal/app/web/handler/org/handler.go` (`OrgStore.CreateMemberUser`, `CreateUserRequest`, `CreateUserResponse`, `CreateUser`), `internal/app/web/handler/user/handler.go` (`ProfileStore`, `Profile`, `GetProfile`), `internal/app/web/handler/user/routes.go` (summary)
- Test: `internal/app/web/handler/org/handler_test.go`, `internal/app/web/handler/user/handler_test.go`, `internal/infra/db/repository/user_test.go`, `internal/app/web/contract/{client,matrix,org,user,docker,harness}_test.go`
- Regenerate: `api/openapi.yaml`, `internal/app/web/contract/testdata/parity.golden.json`

**Interfaces:**
- Consumes: Task 2's `RemoveMember` and org handler.
- Produces: `CreateMemberUser(ctx context.Context, orgID, name, email, tempPassword string) (model.User, error)` on `org.OrgStore`, `port.UserRepository` and `*DbUserRepository`; `org.CreateUserRequest{Name, Email}`; `org.CreateUserResponse{UserID, Name, Email, TempPassword}`; `user.Profile{UserID, Name, Email, MustChangePassword}`; `model.Member{User, Email, Provider}`; `model.RoleOwner`, `model.RoleAdmin` (the only `OrganizationRole` values). Test helpers: `storedRole(t, repo, userID) string` (repository package), `(*env).setStoredRole(t, userID, role string)` (contract package). The JSON key `"role"` no longer appears in `get-profile`, `get-members` or `create-user` responses (web Tasks 4–5 rely on this).

- [ ] **Step 1: Write the failing handler tests**

In `internal/app/web/handler/org/handler_test.go`:
- Replace the fake's `CreateMemberUser` with:

```go
func (f *fakeOrgStore) CreateMemberUser(_ context.Context, orgID, name, email, tempPassword string) (model.User, error) {
	if email == f.takenEmail {
		return model.User{}, model.ErrEmailTaken
	}
	f.created = append(f.created, email+"|"+tempPassword)
	return model.User{ID: "new-1", Name: name}, nil
}
```

- In `TestCreateUserReturnsTempPasswordOnce`, change the body to `` `{"name":"Bob","email":"Bob@X.io"}` `` and replace its final check with:

```go
	if len(f.created) != 1 || f.created[0] != "bob@x.io|"+resp.Data.TempPassword {
		t.Errorf("created = %v", f.created)
	}
	if strings.Contains(w.Body.String(), `"role"`) {
		t.Errorf("response still carries a role: %s", w.Body.String())
	}
}

// A client that still sends a role (a browser tab opened before the
// upgrade) creates the user anyway: the field is ignored, whatever it says.
func TestCreateUserIgnoresStaleRole(t *testing.T) {
	h, f := newHandler()
	for i, role := range []string{"member", "admin", "owner", "weird"} {
		body := `{"name":"B","email":"b` + string(rune('0'+i)) + `@x.io","role":"` + role + `"}`
		if w := post(h.CreateUser, body); w.Code != http.StatusOK {
			t.Errorf("role %q: code = %d, want 200", role, w.Code)
		}
	}
	if len(f.created) != 4 {
		t.Errorf("created = %v, want 4 accounts", f.created)
	}
}
```

- In `TestCreateUserValidation`, replace the bodies list with:

```go
	for _, body := range []string{
		`{}`,
		`{"name":"B","email":""}`,
		`{"name":"B","email":"not-an-email"}`,
		`{"name":"B","email":"taken@x.io"}`,
	} {
```

- In `TestGetMembers`, replace the setup and assertion with:

```go
	f.members = []model.Member{{User: model.User{ID: "u-1", Name: "Ann"}, Email: "ann@x.io", Provider: "local"}}
	r := authed(httptest.NewRequest("GET", "/x", nil))
	w := httptest.NewRecorder()
	h.GetMembers(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ann@x.io") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"role"`) {
		t.Errorf("members still carry a role: %s", w.Body.String())
	}
```

In `internal/app/web/handler/user/handler_test.go`:
- Delete the `role       string` field from `fakeProfileStore` and the method `func (f *fakeProfileStore) RoleOf(context.Context, string) (string, error) { return f.role, nil }`.
- `TestGetProfileShape`: construct `&fakeProfileStore{creds: &model.Credentials{Email: "a@b.io", MustChangePassword: true}}`; delete the `Role string `json:"role"`` field from the anonymous response struct; replace the final check with:

```go
	if resp.Data.UserID != "user-1" || resp.Data.Email != "a@b.io" || !resp.Data.MustChangePassword {
		t.Errorf("profile = %+v", resp.Data)
	}
	if strings.Contains(w.Body.String(), `"role"`) {
		t.Errorf("profile still carries a role: %s", w.Body.String())
	}
```

- `TestGetProfileGoogleOnlyHasNoEmail`: construct `&fakeProfileStore{creds: nil}`.

- [ ] **Step 2: Write the failing repository and contract tests**

In `internal/infra/db/repository/user_test.go`:
- Add after `newUserRepo`:

```go
// storedRole reads organization_users.role straight from the table: the app
// no longer reads it, but it is still written (see model.OrganizationRole).
func storedRole(t *testing.T, repo *DbUserRepository, userID string) string {
	t.Helper()
	var row models.OrganizationUser
	if err := repo.db.GetDB().NewSelect().Model(&row).
		Where("user_id = ?", userID).Scan(context.Background()); err != nil {
		t.Fatalf("read role of %s: %v", userID, err)
	}
	return row.Role
}
```

- Drop the role argument from every `CreateMemberUser` call (five calls, all passing `"member"` right before the password):

```bash
sed -i 's/repo\.CreateMemberUser(ctx, orgID, \("[^"]*"\), \("[^"]*"\), "member", /repo.CreateMemberUser(ctx, orgID, \1, \2, /' internal/infra/db/repository/user_test.go
grep -n 'CreateMemberUser' internal/infra/db/repository/user_test.go   # none may still pass "member"
```

- In `TestBootstrapLocalAdminCreatesOwnerOnce`, replace the `repo.RoleOf(ctx, admin.ID)` check with:

```go
	if role := storedRole(t, repo, admin.ID); role != "owner" {
		t.Errorf("stored role = %q, want owner", role)
	}
```

- In `TestCreateMemberUserJoinsOrgWithoutNewOrg`, replace the `repo.RoleOf(ctx, member.ID)` check with:

```go
	// Admin, not the column default 'member': an older binary run against
	// this database must not demote accounts created without roles.
	if role := storedRole(t, repo, member.ID); role != "admin" {
		t.Errorf("stored role = %q, want admin", role)
	}
```

- In `TestRegisterLocalUserCreatesOwnOrg`, replace the `repo.RoleOf(ctx, a.ID)` check with:

```go
	if role := storedRole(t, repo, a.ID); role != "owner" {
		t.Errorf("stored role = %q, want owner", role)
	}
```

In `internal/app/web/contract/client_test.go` (`member`), change `"name": "Member", "email": "member@example.test", "role": "member",` to `"name": "Member", "email": "member@example.test",`.

In `internal/app/web/contract/matrix_test.go`, change the create-user probe body `map[string]any{"name": "n", "email": "not-an-email", "role": "member"}` to `map[string]any{"name": "n", "email": "not-an-email"}`.

In `internal/app/web/contract/org_test.go` (`members administer`), change `org.CreateUserRequest{Name: "Carol", Email: "carol@example.test", Role: "member"}` to `org.CreateUserRequest{Name: "Carol", Email: "carol@example.test"}`.

In `internal/app/web/contract/user_test.go`, add `"strings"` to the imports and replace the `profile` subtest with:

```go
	t.Run("profile", func(t *testing.T) {
		var p user.Profile
		r := owner.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusOK)
		r.data(t, &p)
		if p.UserID != owner.userID || p.Email != owner.email || p.MustChangePassword {
			t.Fatalf("profile = %+v", p)
		}
		if strings.Contains(string(r.Body), `"role"`) {
			t.Fatalf("profile still carries a role: %s", r.Body)
		}
	})
```

In `internal/app/web/contract/harness_test.go`: add the import `"winterflow/internal/infra/db/models"`; add the field `conn  *db.BunConnection` to `env` (after `cfg`); set it in `newEnv` (`e := &env{ctx: ctx, deps: deps, agent: agent, srv: srv, bus: b, cfg: cfg, conn: conn, requests: map[string]string{}}`); and add before `seedPendingServer`:

```go
// setStoredRole overwrites a user's organization_users.role, to stand in for
// an account created before roles were removed (stored role "member").
func (e *env) setStoredRole(t *testing.T, userID, role string) {
	t.Helper()
	if _, err := e.conn.GetDB().NewUpdate().Model((*models.OrganizationUser)(nil)).
		Set("role = ?", role).Where("user_id = ?", userID).Exec(e.ctx); err != nil {
		t.Fatalf("set stored role: %v", err)
	}
}
```

In `internal/app/web/contract/docker_test.go`, in `members mutate infrastructure`, change the comment and add the downgrade right after `m := owner.member(t)`:

```go
	// No roles: a member runs every docker route the owner does, even one
	// whose stored role is still "member" from before the upgrade.
	t.Run("members mutate infrastructure", func(t *testing.T) {
		m := owner.member(t)
		e.setStoredRole(t, m.userID, "member")
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go vet ./internal/... ; go test ./internal/app/web/contract/`
Expected: FAIL — `go vet` reports compile errors in `handler/org` (`*fakeOrgStore does not implement OrgStore (wrong type for method CreateMemberUser)`), `handler/user` (`*fakeProfileStore does not implement ProfileStore (missing method RoleOf)`) and the repository tests (`not enough arguments in call to repo.CreateMemberUser`). The contract files compile; the suite fails in `TestUserRoutes/profile` (`profile still carries a role`) and in every test using `client.member` (create-user without a role answers 400 `role must be admin or member`).

- [ ] **Step 4: Implement**

In `internal/domain/model/organization.go`, replace everything from `type OrganizationRole string` through the end of `IsAdmin` with:

```go
// OrganizationRole is the value stored in organization_users.role. Winterflow
// has no roles (every member may do everything) and nothing reads the
// column. It is still written explicitly, rather than left to its 'member'
// default, so that an older, role-aware binary run against this database
// treats every new account as an administrator.
type OrganizationRole string

const (
	// RoleOwner marks the account that created the org (claim, self-signup,
	// first OAuth login).
	RoleOwner OrganizationRole = "owner"
	// RoleAdmin marks an account created inside an existing org.
	RoleAdmin OrganizationRole = "admin"
)
```

(keep the existing `Value()` method below it).

In `internal/domain/model/user.go`, delete the line `Role     string `json:"role"`` from `Member`.

In `internal/domain/port/user.go`, change `CreateMemberUser(ctx context.Context, orgID, name, email, role, tempPassword string) (model.User, error)` to `CreateMemberUser(ctx context.Context, orgID, name, email, tempPassword string) (model.User, error)` and delete:

```go
	// RoleOf returns the user's role in their primary organization.
	RoleOf(ctx context.Context, userID string) (string, error)
```

In `internal/infra/db/repository/user.go`:
- Replace the `CreateMemberUser` doc comment and signature with:

```go
// CreateMemberUser creates an account inside an existing organization:
// user + credentials (must-change temp password) + local connected account +
// membership. No personal org. The stored role is model.RoleAdmin (see
// model.OrganizationRole).
func (r *DbUserRepository) CreateMemberUser(ctx context.Context, orgID, name, email, tempPassword string) (model.User, error) {
```

- In its membership insert change `Role:           role,` to `Role:           model.RoleAdmin.Value(),`.
- In `ListMembers` change `m := model.Member{Role: row.Role}` to `var m model.Member`.
- Delete `RoleOf` (from `// RoleOf returns the caller's role in their primary organization.` through its closing brace).

In `internal/app/web/handler/org/handler.go`:
- `OrgStore`: `CreateMemberUser(ctx context.Context, orgID, name, email, tempPassword string) (model.User, error)`.
- Replace `CreateUserRequest` (with its comment) by:

```go
// CreateUserRequest is the body of POST /api/v1/org/create-user.
type CreateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}
```

- Delete `Role         string `json:"role"`` from `CreateUserResponse`.
- In `CreateUser`, delete

```go
	if req.Role != model.RoleAdmin.Value() && req.Role != model.RoleMember.Value() {
		webutil.Error(w, "role must be admin or member", nil)
		return
	}
```

  change the store call to `h.users.CreateMemberUser(r.Context(), orgID, name, email, tempPassword)`, and delete `Role:         req.Role,` from the response literal.

In `internal/app/web/handler/user/handler.go`:
- Delete `RoleOf(ctx context.Context, userID string) (string, error)` from `ProfileStore` and `Role               string `json:"role"`` from `Profile`.
- Replace the `GetProfile` doc comment with:

```go
// GetProfile returns the caller's identity as the UI needs it: id, name and,
// for local accounts, email + the must-change flag (drives the forced
// password-change redirect).
```

- Replace

```go
	role, err := h.users.RoleOf(r.Context(), userID)
	if err != nil {
		h.log.Error("GetProfile: role", "error", err, "user_id", userID)
		role = ""
	}
	profile := Profile{UserID: u.ID, Name: u.Name, Role: role}
```

  with `profile := Profile{UserID: u.ID, Name: u.Name}`.

In `internal/app/web/handler/user/routes.go`, change `Summary: "The caller's profile and role", Tag: "user",` to `Summary: "The caller's profile", Tag: "user",`.

Check nothing role-related survives in Go:

```bash
grep -rn 'RoleOf\|RoleMember\|IsAdmin\|ErrLastOwner\|UpdateMemberRole\|rbac' --include=*.go . 
```

Expected: no output.

- [ ] **Step 5: Regenerate the OpenAPI document and the parity golden**

```bash
go build ./... && go vet ./...
make openapi
go test ./internal/app/web/contract -run TestParity -update
git diff -- internal/app/web/contract/testdata/parity.golden.json
git diff --stat -- api/openapi.yaml
```

Run the `-update` line without `WF_CONTRACT_GUARD=require` in the environment (with it, a `-run` narrowed run fails by design). Expected: the parity golden changes only in `list members`: `"provider": "local",` + `"role": "owner"` become `"provider": "local"`. `api/openapi.yaml` loses about a dozen lines — `role` in the properties and `required` lists of `CreateUserRequestInput`, `CreateUserResponse`, `Member` and `Profile` (the remaining `role:` under `SessionUser` is go-pkgz's `token.User` and stays).

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/app/web/... ./internal/infra/db/repository/`
Expected: PASS. If Docker is available, also run `make test-pg` (contract suite on `postgres:16`; must reproduce the same `parity.golden.json` and accept `admin` under the column's CHECK constraint). Expected: `ok winterflow/internal/app/web/contract`.

- [ ] **Step 7: Commit**

```bash
git add -A internal api/openapi.yaml
git commit -m "refactor(api)!: stop exposing roles

Profile, members and create-user no longer carry a role and RoleOf is
gone. The organization_users.role column stays: creators keep owner,
accounts created in an org store admin so an older binary on the same
database does not demote them. A stale role in create-user is ignored.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Members page without roles

The page loses its Role column, role select, role picker and `update-member` call; the org "Edit" button is shown to everyone. It still reads `useProfile()` for the "(you)" marker.

**Files:**
- Modify: `web/src/pages/org-members.tsx`
- Test (create): `web/src/pages/org-members.test.tsx`

**Interfaces:**
- Consumes: Task 3's API shapes — `GET /api/v1/org/get-members` returns members without `role`; `POST /api/v1/org/create-user` takes `{name, email}`; `org/update-member` no longer exists. `ProfileContext` / `ProfileContextValue` from `@/context/profile-context-base` (unchanged until Task 5; the test only sets `profile`).
- Produces: `OrgMembersPage` no longer reads `isAdmin` (so Task 5 can delete it from the context type).

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/org-members.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  ProfileContext,
  type ProfileContextValue,
} from "@/context/profile-context-base";
import OrgMembersPage from "@/pages/org-members";

// The page sets breadcrumbs through the app layout's outlet context; the
// layout isn't under test.
vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));

const ME = {
  id: "u-me",
  name: "Ann",
  email: "ann@example.test",
  provider: "local",
  last_seen_at: "2026-10-10T12:00:00Z",
  created_at: "2026-10-01T12:00:00Z",
};
const BOB = { ...ME, id: "u-bob", name: "Bob", email: "bob@example.test" };

type Call = { path: string; body: unknown };

// fakeAPI answers the page's fetches by path and records every call.
function fakeAPI() {
  const calls: Call[] = [];
  const data: Record<string, unknown> = {
    "/api/v1/org/get-members": [ME, BOB],
    "/api/v1/org/get-organization": { org_id: "o-1", name: "Homelab", icon: "", color: "#64748b" },
    "/api/v1/org/create-user": { user_id: "u-new", email: "cy@example.test", temp_password: "TEMP-PASS-123456" },
    "/api/v1/org/remove-member": null,
    "/api/v1/org/update-organization": null,
  };
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const path = new URL(url, "http://x").pathname;
    calls.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (!(path in data)) {
      return new Response(JSON.stringify({ success: false, message: "no route" }), { status: 404 });
    }
    return new Response(JSON.stringify({ success: true, message: "ok", data: data[path] }));
  });
  return { calls, fetchMock };
}

function renderPage() {
  const profile = {
    profile: { user_id: ME.id, name: ME.name, email: ME.email, must_change_password: false },
  } as ProfileContextValue;
  return render(
    <ProfileContext.Provider value={profile}>
      <OrgMembersPage />
    </ProfileContext.Provider>,
  );
}

describe("OrgMembersPage", () => {
  let api: ReturnType<typeof fakeAPI>;

  beforeEach(() => {
    api = fakeAPI();
    vi.stubGlobal("fetch", api.fetchMock);
    vi.spyOn(window, "confirm").mockReturnValue(true);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("lists members without roles and lets any member manage them", async () => {
    renderPage();

    const bobRow = (await screen.findByText("Bob")).closest("tr")!;
    expect(screen.queryByRole("columnheader", { name: "Role" })).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    // Everyone else can be reset and removed; you can't remove yourself.
    expect(within(bobRow).getByRole("button", { name: "Reset password" })).toBeInTheDocument();
    expect(within(bobRow).getByRole("button", { name: "Remove" })).toBeInTheDocument();
    const meRow = screen.getByText("(you)").closest("tr")!;
    expect(within(meRow).queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
    // The organization card is editable by every member.
    expect(screen.getByRole("button", { name: /Edit/ })).toBeInTheDocument();
  });

  it("creates a user without a role", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: /Add user/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByText("Role")).not.toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Name"), "Cy");
    await user.type(within(dialog).getByLabelText("Email"), "cy@example.test");
    await user.click(within(dialog).getByRole("button", { name: "Create" }));

    expect(await screen.findByText("User created")).toBeInTheDocument();
    expect(screen.getByDisplayValue("TEMP-PASS-123456")).toBeInTheDocument();
    const created = api.calls.find((c) => c.path === "/api/v1/org/create-user");
    expect(created?.body).toEqual({ name: "Cy", email: "cy@example.test" });
    expect(api.calls.some((c) => c.path === "/api/v1/org/update-member")).toBe(false);
  });

  it("removes another member after confirmation", async () => {
    const user = userEvent.setup();
    renderPage();

    const bobRow = (await screen.findByText("Bob")).closest("tr")!;
    await user.click(within(bobRow).getByRole("button", { name: "Remove" }));

    expect(window.confirm).toHaveBeenCalled();
    const removed = api.calls.find((c) => c.path === "/api/v1/org/remove-member");
    expect(removed?.body).toEqual({ user_id: "u-bob" });
  });

  it("edits the organization", async () => {
    const user = userEvent.setup();
    renderPage();

    await screen.findByText("Homelab");
    await user.click(screen.getByRole("button", { name: /Edit/ }));
    const dialog = await screen.findByRole("dialog");
    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "Lab");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    const saved = api.calls.find((c) => c.path === "/api/v1/org/update-organization");
    expect(saved?.body).toEqual({ name: "Lab", icon: "", color: "#64748b" });
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm --dir web exec vitest run src/pages/org-members.test.tsx`
Expected: FAIL — the first test finds the `Role` column header and a `combobox`, and no `Edit` button (`isAdmin` is undefined in the test's context); the create test finds `Role` in the dialog and a body with `role: "member"`.

- [ ] **Step 3: Remove roles from the page**

In `web/src/pages/org-members.tsx`:
- Delete `role: string;` from the `Member` type.
- Replace the comment above `OrgMembersPage` with:

```tsx
// OrgMembersPage: any member manages the organization — its name/icon/color
// and its users: create accounts with one-time temp passwords, reset
// passwords, remove (anyone but yourself). There are no roles.
```

- `const { profile, isAdmin } = useProfile();` → `const { profile } = useProfile();`
- Delete `const [role, setRole] = useState("member");` and, in `openAdd`, `setRole("member");`.
- In `handleCreate`, change the payload `{ name: name.trim(), email: email.trim(), role },` to `{ name: name.trim(), email: email.trim() },`.
- Delete the whole `handleRole` function (`const handleRole = async (m: Member, newRole: string) => { … };`).
- Replace

```tsx
          {isAdmin ? (
            <Button size="sm" variant="outline" onClick={openOrgEdit}>
              <Pencil /> Edit
            </Button>
          ) : null}
```

  with

```tsx
          <Button size="sm" variant="outline" onClick={openOrgEdit}>
            <Pencil /> Edit
          </Button>
```

- Delete the header cell `<TableHead>Role</TableHead>` and the whole role cell that follows `<TableCell>{m.email || "—"}</TableCell>`:

```tsx
                      <TableCell>
                        {m.role === "owner" || isSelf ? (
                          <span className="capitalize">{m.role}</span>
                        ) : (
                          <select
                            className="bg-transparent text-sm"
                            value={m.role}
                            onChange={(e) => void handleRole(m, e.target.value)}
                          >
                            <option value="admin">admin</option>
                            <option value="member">member</option>
                          </select>
                        )}
                      </TableCell>
```

- In the "Add user" dialog, delete the role picker block:

```tsx
                <div className="space-y-2">
                  <Label>Role</Label>
                  <div className="flex gap-2">
                    {["member", "admin"].map((r) => (
                      <Button
                        key={r}
                        type="button"
                        size="sm"
                        variant={role === r ? "default" : "outline"}
                        onClick={() => setRole(r)}
                      >
                        {r}
                      </Button>
                    ))}
                  </div>
                </div>
```

Then confirm: `grep -n 'role\|isAdmin\|update-member' web/src/pages/org-members.tsx` prints nothing.

- [ ] **Step 4: Run the test to verify it passes**

Run: `pnpm --dir web exec vitest run src/pages/org-members.test.tsx && pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: 4 tests pass; `tsc` and ESLint clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/org-members.tsx web/src/pages/org-members.test.tsx
git commit -m "feat(web): members page without roles

No role column, picker or update-member call; every member can edit the
organization and manage the other members.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Drop `isAdmin` from the web

The profile context no longer computes `isAdmin` and the `Profile` type has no `role`; the nav "Members" item and the Volumes tab's create/delete are shown to everyone.

**Files:**
- Modify: `web/src/context/profile-context-base.ts`, `web/src/context/profile-context.tsx`, `web/src/components/nav-user.tsx`, `web/src/pages/settings-volumes.tsx`
- Test: create `web/src/context/profile-context.test.tsx`, `web/src/components/nav-user.test.tsx`; modify `web/src/pages/settings-volumes.test.tsx`

**Interfaces:**
- Consumes: Task 4 (org-members no longer reads `isAdmin`); Task 3's profile JSON without `role`.
- Produces: `type Profile = { user_id: string; name: string; email: string; must_change_password: boolean }` and `type ProfileContextValue = { profile: Profile | null; loading: boolean; refresh: () => Promise<void> }`. `VolumesCard` no longer calls `useProfile()` (it renders without a `ProfileProvider`).

- [ ] **Step 1: Write the failing tests**

Create `web/src/context/profile-context.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AuthContext, type AuthContextValue } from "@/context/auth-context-base";
import { ProfileProvider } from "@/context/profile-context";
import { useProfile } from "@/context/use-profile";

// Shows exactly what the context hands its consumers.
function Probe() {
  const value = useProfile();
  return <pre data-testid="value">{JSON.stringify(value)}</pre>;
}

function renderProvider(isAuthenticated: boolean) {
  const auth = { isAuthenticated } as AuthContextValue;
  return render(
    <AuthContext.Provider value={auth}>
      <ProfileProvider>
        <Probe />
      </ProfileProvider>
    </AuthContext.Provider>,
  );
}

describe("ProfileProvider", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("exposes the loaded profile and no role or admin flag", async () => {
    const profile = { user_id: "u-1", name: "Ann", email: "ann@example.test", must_change_password: false };
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ success: true, message: "profile", data: profile }))),
    );
    renderProvider(true);

    const value = await screen.findByText(/"user_id":"u-1"/);
    const parsed = JSON.parse(value.textContent ?? "{}");
    expect(parsed.profile).toEqual(profile);
    expect(parsed.loading).toBe(false);
    expect(Object.keys(parsed)).not.toContain("isAdmin");
  });

  it("has no profile when signed out", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    renderProvider(false);

    expect(await screen.findByText(/"loading":false/)).toHaveTextContent('"profile":null');
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
```

Create `web/src/components/nav-user.test.tsx` (imports from `@/components/ui/use-sidebar` are fine; the file is not modified):

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { NavUser } from "@/components/nav-user";
import {
  SidebarContext,
  type SidebarContextProps,
} from "@/components/ui/use-sidebar";
import { AuthContext, type AuthContextValue } from "@/context/auth-context-base";
import {
  ProfileContext,
  type ProfileContextValue,
} from "@/context/profile-context-base";
import { UserContext, type UserContextValue } from "@/context/user-context-base";

function renderNav() {
  const sidebar = { isMobile: false, state: "expanded" } as SidebarContextProps;
  const auth = { logout: vi.fn() } as unknown as AuthContextValue;
  const user = {
    user: { id: "u-1", name: "Ann", picture: "", provider: "local" },
    loading: false,
  } as UserContextValue;
  // A plain account: nothing in the profile says "admin" any more.
  const profile = {
    profile: { user_id: "u-1", name: "Ann", email: "ann@example.test", must_change_password: false },
  } as ProfileContextValue;
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <SidebarContext.Provider value={sidebar}>
        <AuthContext.Provider value={auth}>
          <UserContext.Provider value={user}>
            <ProfileContext.Provider value={profile}>
              <Routes>
                <Route path="/" element={<NavUser />} />
                <Route path="/org/members" element={<p>members page</p>} />
              </Routes>
            </ProfileContext.Provider>
          </UserContext.Provider>
        </AuthContext.Provider>
      </SidebarContext.Provider>
    </MemoryRouter>,
  );
}

describe("NavUser", () => {
  it("offers Members to every user and navigates there", async () => {
    const user = userEvent.setup();
    renderNav();

    await user.click(screen.getByRole("button", { name: /Ann/ }));
    await user.click(await screen.findByRole("menuitem", { name: "Members" }));

    expect(await screen.findByText("members page")).toBeInTheDocument();
  });
});
```

In `web/src/pages/settings-volumes.test.tsx`:
- Delete the import of `ProfileContext` / `ProfileContextValue` from `@/context/profile-context-base`.
- Replace `renderCard` with:

```tsx
// No ProfileContext: the card no longer depends on who is looking.
function renderCard(docker: Docker) {
  const apps = {
    apps: [{ id: "a1", name: "Nextcloud" }],
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={apps}>
        <VolumesCard docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}
```

- Replace the test `hides create and delete from non-admins` with:

```tsx
  it("offers create and delete to every user", async () => {
    renderCard(fakeDocker());

    await screen.findByText("Winterflow apps");
    expect(screen.getByRole("button", { name: /Create volume/ })).toBeInTheDocument();
    // Every unused volume is deletable (the mounted wf-a1_data is not).
    expect(screen.getAllByRole("button", { name: /^Delete volume/ })).toHaveLength(2);
    // The group header spans every column, actions included.
    expect(screen.getByText("Winterflow apps").closest("td")).toHaveAttribute("colspan", "4");
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/context/profile-context.test.tsx src/components/nav-user.test.tsx src/pages/settings-volumes.test.tsx`
Expected: FAIL — profile context: keys contain `isAdmin`; nav: no `Members` menuitem (the profile has no admin role); volumes: every test throws `useProfile must be used within a ProfileProvider`.

- [ ] **Step 3: Remove `isAdmin`**

In `web/src/context/profile-context-base.ts`, replace the `Profile` comment and both types with:

```ts
// Profile is the authenticated user's identity as the API reports it:
// email + must_change_password exist only for accounts with local
// credentials. There are no roles: every user may do everything.
export type Profile = {
  user_id: string;
  name: string;
  email: string;
  must_change_password: boolean;
};

export type ProfileContextValue = {
  profile: Profile | null;
  loading: boolean;
  refresh: () => Promise<void>;
};
```

In `web/src/context/profile-context.tsx`, replace the `value` memo with:

```tsx
  const value = useMemo(
    () => ({ profile, loading, refresh }),
    [profile, loading, refresh],
  );
```

In `web/src/components/nav-user.tsx`: `const { profile, isAdmin } = useProfile();` → `const { profile } = useProfile();`, and replace

```tsx
              {isAdmin ? (
                <DropdownMenuItem onClick={() => navigate("/org/members")}>
                  <Users />
                  Members
                </DropdownMenuItem>
              ) : null}
```

with

```tsx
              <DropdownMenuItem onClick={() => navigate("/org/members")}>
                <Users />
                Members
              </DropdownMenuItem>
```

In `web/src/pages/settings-volumes.tsx`:
- Delete `import { useProfile } from "@/context/use-profile";`, `const { isAdmin } = useProfile();` (first line of `VolumesCard`) and `const cols = isAdmin ? 4 : 3;`.
- In `groupRow`, `colSpan={cols}` → `colSpan={4}`.
- `{isAdmin && <CreateVolumeDialog docker={docker} onCreated={load} />}` → `<CreateVolumeDialog docker={docker} onCreated={load} />`.
- Unwrap the row's action cell — replace `{isAdmin && (` … `)}` around it with the cell itself:

```tsx
          <TableCell className="py-0.5 text-right">
            {/* Docker refuses to remove a mounted volume; don't offer it. */}
            {v.in_use ? null : (
              <Button
                size="icon"
                variant="ghost"
                className="size-8 text-muted-foreground hover:text-destructive"
                aria-label={`Delete volume ${v.name}`}
                title="Delete volume"
                onClick={() => setToDelete(v)}
              >
                <Trash2 className="size-4" />
              </Button>
            )}
          </TableCell>
```

- Unwrap the actions header the same way:

```tsx
                    <TableHead className="w-12">
                      <span className="sr-only">Actions</span>
                    </TableHead>
```

Confirm nothing is left: `grep -rn 'isAdmin\|\.role\b\|role:' web/src --include=*.ts --include=*.tsx | grep -v components/ui` prints nothing.

- [ ] **Step 4: Run the web suite, type check, lint and build**

Run: `pnpm --dir web run test:coverage && pnpm --dir web run lint && pnpm --dir web run build`
Expected: 5 test files, 24 tests pass; ESLint clean; `tsc -b` + `vite build` succeed.

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat(web): show admin controls to every user

The profile no longer carries a role and the context drops isAdmin; the
Members menu item and volume create/delete are shown to everyone.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Docs, full verification, coverage baseline

**Files:**
- Modify: `CLAUDE.md` (line 43 route-table sentence, line 59 Auth paragraph), `MIGRATION.md` (API routes list ~line 304, web pages ~line 320, authentication paragraph ~lines 324–337), `coverage-baseline.json`

**Interfaces:**
- Consumes: all previous tasks.
- Produces: docs matching the code; `coverage-baseline.json` raised to the new web total.

- [ ] **Step 1: Update CLAUDE.md**

In `CLAUDE.md`, replace `` admin-only routes carry `g.Admin` in their `Middleware`. `` with `` `api.Guards` carries only the auth rate limiter (`g.AuthLimit`) — there is no role guard. ``

In the **Auth** paragraph, replace `` or from admins at `/org/members`. Orgs carry name/icon/color (`org/get-organization`, admin `org/update-organization`). `` with `` or from any member at `/org/members`. Orgs carry name/icon/color (`org/get-organization`, `org/update-organization`). `` and replace the sentence

```
Roles: owner/admin administer (members via `/org/members`, server registration, registry/network mutations — gated by `internal/app/web/middleware/rbac`); members keep the full app lifecycle.
```

with

```
No roles: every member of an org may do everything in it (manage members — except removing themselves — register servers, mutate registries/networks/volumes, update the agent, the full app lifecycle). Authorization is authentication plus org ownership of the addressed server (`webutil.RequireServerAccess`). `organization_users.role` is still written (`owner` for an org's creator, `admin` for accounts created in it) but never read.
```

- [ ] **Step 2: Update MIGRATION.md**

Replace

```
  `auth/register` (public), `org/get-organization` (any member), and
  admin-only `org/{create-user,get-members,update-member,remove-member,
  reset-member-password,update-organization}`.
```

with

```
  `auth/register` (public), `org/get-organization` and
  `org/{create-user,get-members,remove-member,reset-member-password,
  update-organization}` (any member; there are no roles).
```

Replace `` (`/user/password`), `org-members` (`/org/members`, admins, incl. the `` with `` (`/user/password`), `org-members` (`/org/members`, every member, incl. the ``.

Replace `` `/register` page) or by an admin: on a fresh instance `` with `` `/register` page) or by any member of an org: on a fresh instance ``.

Replace

```
user_id claim attr). Organizations carry name/icon/color, editable by
admins on `/org/members`; admins manage members there (temp passwords
shown once, forced change on first login); rbac middleware gates org
management, server registration, agent update, and registry/network
mutations to owner/admin.
```

with

```
user_id claim attr). Organizations carry name/icon/color, editable on
`/org/members`, where members are managed too (temp passwords shown
once, forced change on first login). There are no roles: every member
may do everything, including server registration, agent update and
registry/network/volume mutations (the rbac middleware and
`org/update-member` were removed).
```

Check: `grep -rn -i 'rbac\|g\.Admin\|RequireAdmin\|owner/admin\|admin-only' CLAUDE.md AGENTS.md README.md MIGRATION.md` prints only the MIGRATION.md line saying the rbac middleware was removed.

- [ ] **Step 3: Run everything the way CI does**

```bash
gofmt -l $(git ls-files '*.go')
go vet ./...
# make diffcov first runs `make test` (go test -race -coverpkg=./...) and `make web-test`
WF_CONTRACT_GUARD=require make diffcov DIFFCOV_BASE=ci/contract-suite
go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info
pnpm --dir web run lint && pnpm --dir web run build
```

Expected: `gofmt` prints nothing; `go vet` clean; every Go package `ok` (contract coverage guard included — no route lost its happy path, `update-member` left the table with its probe); diff gate `go 43/43 … PASS`, `web 5/5 … PASS` (counts may differ by a line or two; both must be ≥ 80%); ratchet `go 74.0% (baseline 74.0) PASS`, `web ~12.6% (baseline 5.4) PASS` with `::notice::raise coverage-baseline.json web to <X>`. If Docker is available: `make test-pg` → `ok`.

- [ ] **Step 4: Bump the web coverage baseline**

Set `"web"` in `coverage-baseline.json` to the `<X>` printed by the ratchet notice (12.6 at plan time), keeping `"go"` as is:

```json
{ "go": 74.0, "web": 12.6 }
```

Run: `go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info`
Expected: both PASS, no notice.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md MIGRATION.md coverage-baseline.json
git commit -m "docs: every member is an admin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
