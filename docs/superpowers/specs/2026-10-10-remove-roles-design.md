# Remove roles: every user is an admin — design

Date: 2026-10-10. Status: approved in conversation ("Let's simplify access
level: we don't need different roles. Every user is admin."). This is PR 4,
stacked on the CI/contract PRs (#5 → #6 → #7). PR 5 (registered domains)
stacks on it.

## Decision

Winterflow drops the owner/admin/member distinction. Any authenticated user
of an organization can do everything any other user of that organization can.

- **Authorization that stays (fail closed):**
  - authentication (JWT cookie or PAT);
  - org ownership of servers (`webutil.RequireServerAccess`, the
    foreign-org 403);
  - the per-IP rate limit on auth endpoints.
- **Authorization that goes:** role gates. The `adminMW` / `api.Guards.Admin`
  entries on registry, network and volume create/delete, `agent/update`,
  `server/register`, and the `org/*` admin routes; the rbac middleware package;
  and role checks inside handlers or usecases (e.g. the "last owner" or
  "cannot demote" rules), which become moot.
- **Org-member management stays.** It is now available to every member: invite
  or create a user, list members, reset a member's password, remove a member.
  Removing yourself stays refused (you'd lock yourself out). The role-editing
  endpoint (`org/update-member` if it only changes roles) is removed from the
  API along with its UI. If it changes anything other than role, only the role
  part is removed.
- **Data:** the `role` column stays in the database, unused, so nothing needs
  migrating. New users may still get a default value written for
  compatibility. API responses stop exposing `role` (profile and members),
  unless the frontend still needs it for display, in which case drop it there
  too.
- **Web UI:** remove `isAdmin` and role checks. Every control is shown to
  every user, including the Volumes tab's create/delete, the org members page
  and the server registration dialog. Remove the role picker and role badges
  from org members. `web/src/components/ui/**` (vendored shadcn) is never
  touched.
- **Docs:** CLAUDE.md "Auth" paragraph, AGENTS.md if it mentions roles, and
  any route descriptions in the OpenAPI table that mention admin-only. The
  generated `api/openapi.yaml` is regenerated: 403 for "not an admin"
  disappears from those routes, and server-ownership 403s stay.

## Tests

- **Contract suite (PR 3):** the "member gets 403 on admin routes" assertions
  flip to "member succeeds". The foreign-org 403 matrix stays unchanged.
- **Removed role logic:** its handler and usecase tests are deleted with the
  code. Any test that remains asserts the new behaviour, e.g. a member can
  register a server or create a volume.
- **Route golden (`testdata/routes.golden`):** regenerated deliberately, since
  middleware chains lose the rbac entry. That is the one intended change to the
  golden.
- **Gates:** the CI gates apply (80% diff coverage, ratchet). The web code that
  changes is covered by Vitest where it has logic.

## Out of scope

- Multi-org membership and invitations by email.
- Per-app permissions.
- Dropping the `role` column (a later cleanup migration).
