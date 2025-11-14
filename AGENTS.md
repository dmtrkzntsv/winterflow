# Repository Guidelines

## Project Structure & Module Organization

```
web/src/
  components/        # Re-usable presentation & layout primitives
    ui/              # Low-level design-system / shadcn components (button, card …)
    settings-*       # Feature-specific components for Settings page (tabs, sections)
    <feature>-*      # Other feature-scoped components (dashboard-*, catalog-*, …)

  pages/             # Route-level components rendered by React-Router
  hooks/             # Custom React hooks
  contexts/          # React Context providers & related reducers
  lib/               # Framework-agnostic helpers (api, config, routes, utils)
  types/             # Shared TypeScript typings / interfaces
  styles/            # Global styles and Tailwind configuration
```


`cmd/` hosts the Go entrypoints (standalone, API, hub, agent), while shared business logic lives under `internal/` and helper packages under `pkg/`. 
Frontend code resides in `web/`, a Vite + React 19 workspace that uses pnpm, Tailwind 4, and shadcn/ui; assets, hooks, and UI components are co-located under `web/src/`. 
Persistent artifacts such as SQLite files and local certificates live in `data/`. 
Keep tests beside the code they exercise (e.g., `internal/agents/agent_test.go`) to simplify package imports.

## Build, Test, and Development Commands
- `make build` – compiles the standalone and API binaries into `bin/`.
- `make api | make hub | make agent` – runs the selected service with `go run`.
- `make web` – starts the Vite dev server via `pnpm --dir web dev`.
- `pnpm --dir web run build` – type-checks (`tsc -b`) and produces a production bundle.
- `pnpm --dir web run lint` – runs the ESLint suite configured for React 19.
- `make generate-hub-certs` / `make generate-agent-certs` – bootstrap local mTLS material for hub↔agent testing.

## Coding Style & Naming Conventions
Go code must stay `gofmt`-clean and pass `go vet`; prefer descriptive, lowercase package names (`internal/tunnels`). 
Exported Go types use CamelCase, while private identifiers remain lowerCamelCase. 
Frontend code follows TypeScript strict mode with React function components and Tailwind utility classes; co-locate styles via Tailwind tokens instead of ad-hoc CSS files. 
Use PascalCase for components (`AgentList`) and kebab-case for filenames (`agent-list.tsx`) unless the file defines a component (then match its name).

## Testing Guidelines
Unit and integration coverage rely on the standard Go toolchain: run `go test ./...` from the repo root and include new table-driven tests when adding logic in `internal/` or `pkg/`. 
The frontend currently relies on type safety plus ESLint; if you introduce Vitest or Playwright, add scripts to `web/package.json` and document fixtures in this guide. 
Always mention which suites you executed in PR descriptions.
Do not create any tests by default.

## Commit & Pull Request Guidelines
The Git history follows Conventional Commits (`feat:`, `fix:`, `refactor:`). 
Keep messages imperative (“feat: add tunnel watcher”) and scope them narrowly. 
PRs should describe motivation, summarize code changes, list validation commands, and link issues or Linear tickets. 
Include screenshots or recordings for UI-facing updates (Vite dev server output, component previews) and note any configuration steps contributors must repeat.

## Security & Configuration Tips
Never commit secrets or TLS keys: `.gitignore` already excludes build outputs, but confirm new artifacts stay out of version control. 
Use the provided `generate-*-certs` targets for local mTLS needs, and store `.env` values under `data/` or in your shell, not in source. 
When reviewing contributions, ensure new tunnels, agents, or CLI flags respect the 1-user-per-org model and keep Redis/PostgreSQL credentials confined to configuration files.
