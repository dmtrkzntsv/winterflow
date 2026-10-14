# UX rebuild, phase 1: foundation and shell — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lay the foundation of the UX rebuild: status tokens and a Light/Dark/System theme, the shared primitives (PageHeader, StatusPill/StatusDot, ConfirmDialog, EmptyState, ResponsiveDialog, Section/RowList, Disclosure, underline tabs synced to `?tab=`), the new shell (rebuilt sidebar with server switcher, search, live app list, Inbox badge; no desktop breadcrumbs; a mobile top bar; the ⌘K palette), and the new route table with redirects from every old URL. Existing page bodies are mounted at their new paths unchanged.

**Architecture:** Pure helpers in `web/src/lib/` (`routes.ts`, `settings-nav.ts`, `status.ts`, `theme.ts`, `inbox.ts`, `palette.ts`, `keyboard.ts`, `nav.ts`) carry the logic and are tested directly. Small external stores (`useSyncExternalStore`) hold the theme choice and the Inbox last-seen time so every consumer re-renders on a change made anywhere, including another tab. Two contexts are new: a page-title context (replaces the outlet-context breadcrumbs: pages call `usePageTitle`, the mobile top bar and `document.title` read it) and a command-palette context (open state plus a registry pages fill with contextual actions). The route table moves out of `main.tsx` into `web/src/routes.tsx` so it can be tested with `createMemoryRouter`; old URLs are `LegacyRedirect` routes.

**Tech Stack:** React 19, React Router 7 (data router), TypeScript 5.9 (strict, `verbatimModuleSyntax`, `erasableSyntaxOnly`), Vite 7, Tailwind 4 (tokens in `web/src/index.css`), vendored shadcn/ui (Radix, cmdk 1.1, vaul 1.1), Vitest 5 + Testing Library on jsdom. No new dependency, no Go change.

**Spec:** `docs/superpowers/specs/2026-10-13-ux-rebuild-design.md` — this plan is phase 1 of §8 ("Foundation + shell"): §1 "Look" and "Shared primitives", §2 "Navigation and shell" including the Routes table and redirects, and the theme toggle. Phases 2–7 redesign the page bodies on top of the interfaces this plan produces (listed in "Interfaces for later phases" at the end).

**Base:** branch `feat/ux-rebuild` at `2a7ae72` (`docs: UX rebuild design spec`). All commands run from the worktree root.

## Global Constraints

- The spec is binding. Phase 1 does **not** redesign page bodies (Apps page, app page, create flows, monitoring, Inbox content, settings pages, templates, sign-in): existing pages are mounted at their new paths with only the changes this plan lists (paths, page title, tab wrapper on the app page, `section` prop on the org page).
- Frontend only: no Go change, no API change; `api/openapi.yaml` stays untouched. No feature is removed: every page reachable before is reachable after (old URLs redirect).
- Never delete anything under `web/src/components/ui/` (vendored shadcn/ui, kept on purpose even when unused). Editing vendored files is not needed by this plan; do not.
- Keep the Frost palette, Inter, the sky wash, and the snowflake logo: `web/src/components/app-logo.tsx` is not edited and the logo is never restyled.
- Status colours are theme tokens `--status-ok|warn|bad|idle` (Tailwind `bg-status-ok`, `text-status-bad`, …), never new hard-coded status colours (`bg-emerald-500`, `bg-red-500`, …).
- Type scale: page title 24/600 (`text-2xl font-semibold tracking-tight`), section title 15/600 (`text-[15px] font-semibold`), body 14 (`text-sm`); spacing on the 4/8 grid; reading pages capped at 1100px (`max-w-[1100px]`), the app page and Monitoring full width; transitions 150 ms (`duration-150`); anything that animates also has `motion-reduce:` off.
- Theme: Light / Dark / System, default System, stored in `localStorage["wf.theme"]` (absent = System).
- Breakpoints: the shell is "mobile" below 1024px (`useIsMobile`); dialogs become bottom sheets below 640px (`ResponsiveDialog`). jsdom has no `matchMedia`: every hook reads that as desktop, and tests stub phones with `stubViewport(375)` from `web/src/test/media.ts`.
- Tap targets in the mobile chrome are at least 40px (`size-10`, `min-h-10`, `max-lg:h-10`).
- Real router links everywhere (`<Link>`, `navigate`); no `href="#"`, no full reloads; no `window.confirm` in new code (destructive confirms go through `ConfirmDialog`).
- `localStorage` keys: `wf.theme`, `wf.inbox.seen.<userId>`, `wf.palette.recent`. Every read/write is wrapped in `try`/`catch` (storage can be blocked).
- i18n: only the existing auth strings go through i18next (`t("auth.logout")`); everything else is a plain English literal, like every existing page.
- Lint (`eslint-plugin-react-hooks` 7): no impure calls during render (`Date.now()`, `Math.random()` only in effects/handlers — `react-hooks/purity`); no `ref.current` reads during render. `react-refresh`: files that export components export only components — contexts go in `*-base.ts`, hooks in `use-*.ts` (the existing `context/` pattern).
- TypeScript: `import type` for types (`verbatimModuleSyntax`), no `enum` (`erasableSyntaxOnly`), no unused locals/imports (`noUnusedLocals`) — when a step removes the last use of an import, remove the import.
- Tests: Vitest + Testing Library, files beside the code. Mock contexts the way existing tests do (`vi.mock("@/context/use-servers", …)`, `vi.mock("@/context/use-apps", …)`); answer fetches with `fakeAPI` from `@/test/fake-api`; keep existing `data-testid`s.
- Coverage: changed web lines must be ≥80% covered (`tools/diffcov`); `web/src/main.tsx`, `web/src/test/`, `*.test.tsx` and `web/src/components/ui/` are exempt.
- `pnpm --dir web run build` empties `web/dist`: run `git checkout -- web/dist/.gitkeep` after any build, before committing.
- Per task, before the commit: the task's Vitest files, `pnpm --dir web run lint`, and `pnpm --dir web exec tsc -b` all pass.
- Commits: Conventional Commits (`feat(web): …`, `refactor(web): …`, `test(web): …`), each message ending with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Old URLs from bookmarks, alert e-mails and other open tabs** (`/org/alerts?tab=rules`, `/server/x/settings?tab=volumes`, `/server/x/create-app/template`, `/user/password` for a user forced to change it, `/org/templates/t1#…`, and the unchanged `/server/x/app/a?tab=metrics` and `/server/x/monitoring` the Go alert messages build). Expected: each lands on its new page in one hop, without a stale `?tab=`, keeping the hash, never on "Page not found", and a `must_change_password` user is not bounced in a loop. Pinned in Task 9 (`routes.test.tsx` redirect table, deep links, must-change).
2. **Switching servers while on a server-only settings page** (Domains open, the other server has no ingress) or on the app page. Expected: Domains falls back to that server's General page instead of a blank or broken page; an app page goes to the new server's Apps. Pinned in Task 7 ("opens General instead on a server without ingress") with the existing `servers-context.test.tsx` switching tests.
3. **A phone opening `/settings`** (the sidebar's Settings entry). Expected: the section list, not an immediate redirect that the first (desktop-default) render would cause; a desktop opening it lands on the active server's General page (Organization without a server). Pinned in Task 4 (`useMediaQuery` answers on the first render) and Task 7 (settings layout: phone list, desktop redirect).
4. **The Inbox unread count across a first visit, another tab, a server clock ahead of the browser, and blocked storage.** Expected: a first visit shows 0 (old history is not "unread"); "Mark all as read" in one tab clears the badge in the other; an event stamped in the browser's future is still marked read; blocked storage shows 0 instead of throwing. Pinned in Task 8 (`use-inbox-unread.test.tsx`, `inbox.test.ts`).
5. **Recent palette items that no longer exist** (a deleted app, an app of another server, a corrupted `wf.palette.recent`). Expected: they are hidden instead of linking to "App not found"; garbage in storage reads as no recent items. Pinned in Task 11 (`palette.test.ts` `visibleRecent`, `readRecent`) and Task 13 (palette hides a gone app).

## File map

Created:

| File | Responsibility |
|---|---|
| `web/src/lib/status.ts` | app/server/backup state → `{ tone, label, pulse }`, tone → token classes |
| `web/src/components/status.tsx` | `StatusDot`, `StatusPill` |
| `web/src/hooks/use-theme.ts` | `useThemePreference()` |
| `web/src/components/themed-toaster.tsx` | the Toaster in the theme on screen |
| `web/src/layouts/page-title-base.ts`, `page-title.tsx`, `use-page-title.ts` | page-title context, provider, `usePageTitle` / `useCurrentPageTitle` |
| `web/src/components/page-header.tsx` | `PageHeader` |
| `web/src/components/section.tsx` | `Section`, `RowList`, `Row` |
| `web/src/components/empty-state.tsx` | `EmptyState` |
| `web/src/components/disclosure.tsx` | `Disclosure` |
| `web/src/hooks/use-media-query.ts` | `useMediaQuery` (synchronous first answer) |
| `web/src/test/media.ts` | `stubViewport(width)` for tests |
| `web/src/components/confirm-dialog.tsx` | `ConfirmDialog` |
| `web/src/components/responsive-dialog.tsx` | `ResponsiveDialog` |
| `web/src/lib/routes.ts` | path builders, legacy-URL mappers, `isWidePath` |
| `web/src/lib/settings-nav.ts` | the Settings sub-menu model |
| `web/src/components/underline-tabs.tsx` | `UnderlineTabs` |
| `web/src/hooks/use-tab-param.ts` | `useTabParam` (`?tab=` sync with aliases) |
| `web/src/hooks/use-settings-sections.ts` | `useSettingsSections()` |
| `web/src/layouts/settings-layout.tsx` | `SettingsLayout`, `SettingsIndex` |
| `web/src/pages/server-settings.tsx` (renamed from `settings.tsx`) | General / Domains / Storages / Docker pages |
| `web/src/pages/settings-notifications.tsx` | channels + alert rules on one page |
| `web/src/pages/settings-shared-variables.tsx` | org + this server's shared variables |
| `web/src/lib/inbox.ts` | last-seen store, unread counting |
| `web/src/hooks/use-inbox-unread.ts` | `useInboxUnread()` |
| `web/src/pages/inbox.tsx` | `/inbox` (alert history for now) |
| `web/src/components/legacy-redirect.tsx` | `LegacyRedirect` |
| `web/src/pages/new-app.tsx` | `/server/:id/new` (`?from=`) |
| `web/src/routes.tsx` | `appRoutes` (the route table) |
| `web/src/lib/keyboard.ts` | shortcut predicates and labels |
| `web/src/lib/palette.ts` | palette entries, recent items |
| `web/src/context/palette-context-base.ts`, `palette-context.tsx`, `use-palette.ts` | palette open state + action registry |
| `web/src/components/command-palette.tsx` | the ⌘K palette |
| `web/src/components/sidebar-link.tsx`, `sidebar-search.tsx` | sidebar link that closes the sheet; search box |
| `web/src/hooks/use-server-shortcuts.ts` | ⌘1…⌘9 |

Deleted: `web/src/layouts/use-app-layout.ts`, `web/src/pages/org-alerts.tsx` (+ test), `web/src/components/nav-main.tsx`, `web/src/components/nav-secondary.tsx`, `web/src/components/sidebar-nav.test.tsx`.

---

### Task 1: Theme preference (Light / Dark / System)

**Files:**
- Modify: `web/src/lib/theme.ts` (replace `followSystemTheme`)
- Modify: `web/src/lib/theme.test.ts` (rewrite)
- Create: `web/src/hooks/use-theme.ts`, `web/src/hooks/use-theme.test.tsx`
- Create: `web/src/components/themed-toaster.tsx`, `web/src/components/themed-toaster.test.tsx`
- Modify: `web/src/main.tsx` (start the theme, themed toaster)
- Modify: `web/index.html` (pre-paint script reads the stored choice)
- Modify: `web/src/hooks/use-dark-mode.ts` (comment only)

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `type ThemePreference = "light" | "dark" | "system"`, `const THEME_KEY = "wf.theme"` (`@/lib/theme`)
  - `getThemePreference(): ThemePreference`, `setThemePreference(p: ThemePreference): void`, `subscribeThemePreference(onChange: () => void): () => void`, `resolveTheme(p, systemDark: boolean): "light" | "dark"`, `startTheme(): () => void` (`@/lib/theme`); `isDarkMode()` and `subscribeDarkMode()` stay as they are.
  - `useThemePreference(): [ThemePreference, (p: ThemePreference) => void]` (`@/hooks/use-theme`)
  - `ThemedToaster` (`@/components/themed-toaster`)

- [ ] **Step 1: Write the failing tests**

Replace `web/src/lib/theme.test.ts` with:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  THEME_KEY,
  getThemePreference,
  resolveTheme,
  setThemePreference,
  startTheme,
  subscribeThemePreference,
} from "./theme"

type Listener = () => void

function mockMatchMedia(initial: boolean) {
  const listeners = new Set<Listener>()
  const mql = {
    matches: initial,
    addEventListener: (_: string, l: Listener) => listeners.add(l),
    removeEventListener: (_: string, l: Listener) => listeners.delete(l),
  }
  vi.stubGlobal("matchMedia", vi.fn(() => mql))
  return {
    set(matches: boolean) {
      mql.matches = matches
      listeners.forEach((l) => l())
    },
    listeners,
  }
}

const dark = () => document.documentElement.classList.contains("dark")

describe("theme preference", () => {
  let stop: () => void = () => {}

  beforeEach(() => localStorage.clear())
  afterEach(() => {
    stop()
    stop = () => {}
    vi.unstubAllGlobals()
    document.documentElement.classList.remove("dark")
    localStorage.clear()
  })

  it("follows the system by default and tracks its changes", () => {
    const media = mockMatchMedia(true)
    stop = startTheme()
    expect(getThemePreference()).toBe("system")
    expect(dark()).toBe(true)

    media.set(false)
    expect(dark()).toBe(false)
  })

  it("keeps an explicit choice over the system and stores it", () => {
    const media = mockMatchMedia(true)
    stop = startTheme()
    setThemePreference("light")
    expect(dark()).toBe(false)
    expect(localStorage.getItem(THEME_KEY)).toBe("light")

    media.set(true)
    expect(dark()).toBe(false)
  })

  it("reads a stored choice at start", () => {
    mockMatchMedia(false)
    localStorage.setItem(THEME_KEY, "dark")
    stop = startTheme()
    expect(getThemePreference()).toBe("dark")
    expect(dark()).toBe(true)
  })

  it("forgets the stored value when going back to the system", () => {
    mockMatchMedia(true)
    localStorage.setItem(THEME_KEY, "light")
    stop = startTheme()
    setThemePreference("system")
    expect(localStorage.getItem(THEME_KEY)).toBeNull()
    expect(dark()).toBe(true)
  })

  it("reads a stored value it doesn't know as the system", () => {
    mockMatchMedia(false)
    localStorage.setItem(THEME_KEY, "purple")
    stop = startTheme()
    expect(getThemePreference()).toBe("system")
  })

  it("picks up a choice made in another tab and tells subscribers", () => {
    mockMatchMedia(false)
    stop = startTheme()
    const seen = vi.fn()
    const unsubscribe = subscribeThemePreference(seen)
    window.dispatchEvent(new StorageEvent("storage", { key: THEME_KEY, newValue: "dark" }))
    expect(getThemePreference()).toBe("dark")
    expect(dark()).toBe(true)
    expect(seen).toHaveBeenCalled()
    unsubscribe()
  })

  it("tells subscribers about a change made here", () => {
    mockMatchMedia(false)
    stop = startTheme()
    const seen = vi.fn()
    const unsubscribe = subscribeThemePreference(seen)
    setThemePreference("dark")
    expect(seen).toHaveBeenCalledTimes(1)
    unsubscribe()
    setThemePreference("light")
    expect(seen).toHaveBeenCalledTimes(1)
  })

  it("stops listening when stopped", () => {
    const media = mockMatchMedia(false)
    startTheme()()
    expect(media.listeners.size).toBe(0)
  })

  it("is light without matchMedia", () => {
    stop = startTheme()
    expect(dark()).toBe(false)
  })
})

describe("resolveTheme", () => {
  it("resolves the system preference from the system", () => {
    expect(resolveTheme("system", true)).toBe("dark")
    expect(resolveTheme("system", false)).toBe("light")
    expect(resolveTheme("light", true)).toBe("light")
    expect(resolveTheme("dark", false)).toBe("dark")
  })
})
```

Create `web/src/hooks/use-theme.test.tsx`:

```tsx
import { act, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it } from "vitest"

import { useThemePreference } from "@/hooks/use-theme"
import { THEME_KEY, setThemePreference } from "@/lib/theme"

function Probe() {
  const [theme, setTheme] = useThemePreference()
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <button onClick={() => setTheme("dark")}>dark</button>
    </div>
  )
}

describe("useThemePreference", () => {
  afterEach(() => {
    act(() => setThemePreference("system"))
    localStorage.clear()
    document.documentElement.classList.remove("dark")
  })

  it("reads and changes the preference", async () => {
    render(<Probe />)
    expect(screen.getByTestId("theme")).toHaveTextContent("system")
    await userEvent.setup().click(screen.getByRole("button", { name: "dark" }))
    expect(screen.getByTestId("theme")).toHaveTextContent("dark")
    expect(localStorage.getItem(THEME_KEY)).toBe("dark")
    expect(document.documentElement).toHaveClass("dark")
  })

  it("re-renders on a change made elsewhere", () => {
    render(<Probe />)
    act(() => setThemePreference("light"))
    expect(screen.getByTestId("theme")).toHaveTextContent("light")
  })
})
```

Create `web/src/components/themed-toaster.test.tsx`:

```tsx
import { act, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { ThemedToaster } from "@/components/themed-toaster"

vi.mock("@/components/ui/sonner", () => ({
  Toaster: ({ theme }: { theme?: string }) => <p>toaster {theme}</p>,
}))

describe("ThemedToaster", () => {
  afterEach(() => document.documentElement.classList.remove("dark"))

  it("follows the theme on screen", async () => {
    render(<ThemedToaster />)
    expect(screen.getByText("toaster light")).toBeInTheDocument()
    act(() => document.documentElement.classList.add("dark"))
    expect(await screen.findByText("toaster dark")).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/theme.test.ts src/hooks/use-theme.test.tsx src/components/themed-toaster.test.tsx`
Expected: FAIL — `startTheme`/`setThemePreference` are not exported, `@/hooks/use-theme` and `@/components/themed-toaster` do not exist.

- [ ] **Step 3: Implement**

Replace the `followSystemTheme` function and its comment at the top of `web/src/lib/theme.ts` (keep `isDarkMode` and `subscribeDarkMode` below it unchanged) with:

```ts
/**
 * Light, dark, or the system's: the user's choice lives in localStorage
 * (THEME_KEY; absent = follow the system) and shows as shadcn's `.dark`
 * class on <html>. index.html applies it before the first paint; startTheme
 * keeps it in step with the system and with other tabs.
 */
export type ThemePreference = "light" | "dark" | "system"

export const THEME_KEY = "wf.theme"

const listeners = new Set<() => void>()
// current is the preference in force; null until first read.
let current: ThemePreference | null = null

function parse(value: string | null): ThemePreference {
  return value === "light" || value === "dark" ? value : "system"
}

function readStored(): ThemePreference {
  try {
    return parse(window.localStorage.getItem(THEME_KEY))
  } catch {
    return "system" // storage blocked: follow the system
  }
}

function systemIsDark(): boolean {
  return (
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches
  )
}

/** resolveTheme is the theme a preference shows. */
export function resolveTheme(
  preference: ThemePreference,
  systemDark: boolean,
): "light" | "dark" {
  if (preference === "system") return systemDark ? "dark" : "light"
  return preference
}

function apply(): void {
  const theme = resolveTheme(getThemePreference(), systemIsDark())
  document.documentElement.classList.toggle("dark", theme === "dark")
}

function emit(): void {
  listeners.forEach((l) => l())
}

/** getThemePreference is the preference in force (a useSyncExternalStore snapshot). */
export function getThemePreference(): ThemePreference {
  current ??= readStored()
  return current
}

/** setThemePreference stores and applies a choice; "system" forgets the stored one. */
export function setThemePreference(preference: ThemePreference): void {
  current = preference
  try {
    if (preference === "system") window.localStorage.removeItem(THEME_KEY)
    else window.localStorage.setItem(THEME_KEY, preference)
  } catch {
    // Storage blocked: the choice holds for this page only.
  }
  apply()
  emit()
}

/** subscribeThemePreference calls onChange after every change, here or in another tab. */
export function subscribeThemePreference(onChange: () => void): () => void {
  listeners.add(onChange)
  return () => {
    listeners.delete(onChange)
  }
}

/**
 * startTheme applies the stored preference and keeps it applied: a system
 * change matters while the preference is "system", and a choice made in
 * another tab arrives as a storage event. Returns a function that stops
 * (used by tests).
 */
export function startTheme(): () => void {
  current = readStored()
  apply()
  const media =
    typeof window.matchMedia === "function"
      ? window.matchMedia("(prefers-color-scheme: dark)")
      : null
  const onSystem = () => {
    if (getThemePreference() === "system") apply()
  }
  const onStorage = (event: StorageEvent) => {
    if (event.key !== THEME_KEY) return
    current = parse(event.newValue)
    apply()
    emit()
  }
  media?.addEventListener("change", onSystem)
  window.addEventListener("storage", onStorage)
  return () => {
    media?.removeEventListener("change", onSystem)
    window.removeEventListener("storage", onStorage)
  }
}
```

Create `web/src/hooks/use-theme.ts`:

```ts
import { useSyncExternalStore } from "react"

import {
  getThemePreference,
  setThemePreference,
  subscribeThemePreference,
  type ThemePreference,
} from "@/lib/theme"

const serverSnapshot = (): ThemePreference => "system"

// useThemePreference is the user's Light/Dark/System choice and its setter;
// every caller re-renders when it changes (here or in another tab).
export function useThemePreference(): [ThemePreference, (preference: ThemePreference) => void] {
  const preference = useSyncExternalStore(subscribeThemePreference, getThemePreference, serverSnapshot)
  return [preference, setThemePreference]
}
```

Create `web/src/components/themed-toaster.tsx`:

```tsx
import { Toaster } from "@/components/ui/sonner"
import { useDarkMode } from "@/hooks/use-dark-mode"

// ThemedToaster is the app's Toaster in the theme on screen: the vendored
// Toaster asks next-themes, which this app doesn't use, so the theme is
// passed in (its props win over its own guess).
export function ThemedToaster() {
  return <Toaster theme={useDarkMode() ? "dark" : "light"} />
}
```

In `web/src/hooks/use-dark-mode.ts` change the comment `(set by followSystemTheme)` to `(set by lib/theme)`.

In `web/src/main.tsx`:
- replace `import { Toaster } from "@/components/ui/sonner"` with `import { ThemedToaster } from "@/components/themed-toaster"`, and `<Toaster />` with `<ThemedToaster />`;
- replace `import { followSystemTheme } from "@/lib/theme"` with `import { startTheme } from "@/lib/theme"`;
- replace

```tsx
// Dark mode follows the OS; set before the first render so nothing flashes.
followSystemTheme()
```

with

```tsx
// Light, dark or the system's (the user menu and ⌘K switch it); applied
// before the first render so nothing flashes.
startTheme()
```

In `web/index.html` replace the inline `<script>…</script>` in `<head>` with:

```html
    <script>
      // The theme before first paint (startTheme in main.tsx then keeps it):
      // the stored choice (wf.theme), else the system's, so nobody sees a
      // flash of the other theme.
      (function () {
        var stored = null;
        try {
          stored = localStorage.getItem("wf.theme");
        } catch (e) {}
        if (
          stored === "dark" ||
          (stored !== "light" &&
            window.matchMedia("(prefers-color-scheme: dark)").matches)
        )
          document.documentElement.classList.add("dark");
      })();
    </script>
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/theme.test.ts src/hooks/use-theme.test.tsx src/components/themed-toaster.test.tsx src/hooks/use-dark-mode.test.ts`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/theme.ts web/src/lib/theme.test.ts web/src/hooks/use-theme.ts web/src/hooks/use-theme.test.tsx web/src/components/themed-toaster.tsx web/src/components/themed-toaster.test.tsx web/src/hooks/use-dark-mode.ts web/src/main.tsx web/index.html
git commit -m "$(cat <<'EOF'
feat(web): Light, Dark and System theme, stored per browser

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Status tokens, StatusDot and StatusPill

**Files:**
- Modify: `web/src/index.css` (status tokens, Tailwind colours, reduced-motion snowfall)
- Create: `web/src/lib/status.ts`, `web/src/lib/status.test.ts`
- Create: `web/src/components/status.tsx`, `web/src/components/status.test.tsx`
- Modify: `web/src/components/app-status-badge.tsx` (tokens; rename its `StatusDot` to `DotLabel`)
- Modify: `web/src/pages/settings-volumes.tsx` (use `DotLabel` and tokens)
- Modify: `web/src/components/template-instances.test.tsx:106`

**Interfaces:**
- Consumes: `AppStatusCode` (`@/context/apps-context-base`), `ServerLiveness` (`@/context/servers-context-base`), `BackupState`, `STATE_LABELS` (`@/lib/backups`).
- Produces:
  - `type StatusTone = "ok" | "warn" | "bad" | "idle"`, `type StatusInfo = { tone: StatusTone; label: string; pulse?: boolean }` (`@/lib/status`)
  - `appStatus(code: number | undefined): StatusInfo`, `serverStatus(opts: { liveness?: ServerLiveness; lastSeenAt: string | null; standalone: boolean }): StatusInfo`, `backupStatus(state: BackupState): StatusInfo`, `TONE_DOT_CLASS: Record<StatusTone, string>`, `TONE_PILL_CLASS: Record<StatusTone, string>` (`@/lib/status`)
  - `StatusDot({ tone, pulse?, label?, className? })`, `StatusPill({ status: StatusInfo, className? })` (`@/components/status`)
  - `DotLabel({ label, dotClassName, className? })` (renamed from `StatusDot` in `@/components/app-status-badge`), `AppStatusIndicator` unchanged API.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/status.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { AppStatusCode } from "@/context/apps-context-base";
import { appStatus, backupStatus, serverStatus } from "@/lib/status";

describe("appStatus", () => {
  it.each([
    [AppStatusCode.Active, "ok", "Running"],
    [AppStatusCode.Idle, "warn", "Idle"],
    [AppStatusCode.Restarting, "warn", "Restarting"],
    [AppStatusCode.Problematic, "bad", "Problematic"],
    [AppStatusCode.Stopped, "idle", "Stopped"],
    [AppStatusCode.Unknown, "idle", "Unknown"],
  ])("maps code %i to %s %s", (code, tone, label) => {
    expect(appStatus(code)).toMatchObject({ tone, label });
  });

  it("reads a missing or unknown code as Unknown, never as down", () => {
    expect(appStatus(undefined)).toEqual({ tone: "idle", label: "Unknown" });
    expect(appStatus(42)).toEqual({ tone: "idle", label: "Unknown" });
  });

  it("pulses only while restarting", () => {
    expect(appStatus(AppStatusCode.Restarting).pulse).toBe(true);
    expect(appStatus(AppStatusCode.Active).pulse).toBeFalsy();
  });
});

describe("serverStatus", () => {
  it("calls the embedded server of a standalone install this server", () => {
    expect(serverStatus({ liveness: undefined, lastSeenAt: null, standalone: true })).toEqual({
      tone: "ok",
      label: "This server",
    });
  });

  it("is online while the agent is connected", () => {
    expect(serverStatus({ liveness: "online", lastSeenAt: null, standalone: false })).toEqual({ tone: "ok", label: "Online" });
  });

  it("tells a server that never connected from one that went away", () => {
    expect(serverStatus({ liveness: undefined, lastSeenAt: null, standalone: false })).toEqual({
      tone: "idle",
      label: "Never connected",
    });
    expect(serverStatus({ liveness: "unknown", lastSeenAt: "2026-10-01T00:00:00Z", standalone: false })).toEqual({
      tone: "bad",
      label: "Offline",
    });
  });
});

describe("backupStatus", () => {
  it.each([
    ["ok", "ok", "OK"],
    ["running", "ok", "Running"],
    ["never", "idle", "Not run yet"],
    ["off", "idle", "Off"],
    ["late", "warn", "Late"],
    ["failing", "bad", "Failing"],
  ] as const)("maps %s to %s %s", (state, tone, label) => {
    expect(backupStatus(state)).toMatchObject({ tone, label });
  });
});
```

Create `web/src/components/status.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { StatusDot, StatusPill } from "@/components/status";

describe("StatusDot", () => {
  it("is an image named by its label in the tone's colour", () => {
    render(<StatusDot tone="ok" label="Running" />);
    const dot = screen.getByRole("img", { name: "Running" });
    expect(dot).toHaveClass("bg-status-ok");
    expect(dot).toHaveAttribute("data-tone", "ok");
  });

  it("is hidden from assistive tech without a label", () => {
    const { container } = render(<StatusDot tone="bad" />);
    const dot = container.querySelector("[data-tone]");
    expect(dot).toHaveAttribute("aria-hidden", "true");
    expect(dot).toHaveClass("bg-status-bad");
  });

  it("pulses unless the user asked for less motion", () => {
    const { container } = render(<StatusDot tone="warn" pulse />);
    expect(container.querySelector("[data-tone]")).toHaveClass("animate-pulse", "motion-reduce:animate-none");
  });
});

describe("StatusPill", () => {
  it("shows the word with a dot of its tone", () => {
    const { container } = render(<StatusPill status={{ tone: "warn", label: "Late" }} />);
    expect(screen.getByText("Late")).toHaveAttribute("data-tone", "warn");
    expect(container.querySelector("span[data-tone] > span[data-tone]")).toHaveClass("bg-status-warn");
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/status.test.ts src/components/status.test.tsx`
Expected: FAIL — modules `@/lib/status` and `@/components/status` not found.

- [ ] **Step 3: Implement**

In `web/src/index.css`:

1. In the `:root` block, after `--sidebar-bottom: #edf3fa;` add:

```css
    /* Status tones (lib/status): one token each, so a status means the same
       colour everywhere. Dots need >= 3:1 against the card. */
    --status-ok: #1d8a5f;
    --status-warn: #b7770c;
    --status-bad: #d03a3a;
    --status-idle: #8ea3ba;
```

2. In the `.dark` block, after `--sidebar-bottom: #0a1522;` add:

```css
    --status-ok: #3cc48e;
    --status-warn: #e3aa45;
    --status-bad: #ff7d73;
    --status-idle: #5d7590;
```

3. In `@theme inline`, after `--color-skeleton-shine: var(--skeleton-shine);` add:

```css
    --color-status-ok: var(--status-ok);
    --color-status-warn: var(--status-warn);
    --color-status-bad: var(--status-bad);
    --color-status-idle: var(--status-idle);
```

4. Replace the final reduced-motion block with:

```css
@media (prefers-reduced-motion: reduce) {
    .frost,
    .app-card-in {
        animation: none;
    }
    /* The login page's snowfall is decoration: gone for less motion. */
    .login-snowflake {
        display: none;
    }
}
```

Create `web/src/lib/status.ts`:

```ts
import { AppStatusCode } from "@/context/apps-context-base";
import type { ServerLiveness } from "@/context/servers-context-base";
import { STATE_LABELS, type BackupState } from "@/lib/backups";

// StatusTone is the colour family of a state: one token each in index.css
// (--status-ok|warn|bad|idle), so the same state looks the same on every
// page.
export type StatusTone = "ok" | "warn" | "bad" | "idle";

// StatusInfo is a state as the UI shows it: the tone, the word, and whether
// it is a moving state (pulses).
export type StatusInfo = { tone: StatusTone; label: string; pulse?: boolean };

export const TONE_DOT_CLASS: Record<StatusTone, string> = {
  ok: "bg-status-ok",
  warn: "bg-status-warn",
  bad: "bg-status-bad",
  idle: "bg-status-idle",
};

export const TONE_PILL_CLASS: Record<StatusTone, string> = {
  ok: "bg-status-ok/15 text-foreground",
  warn: "bg-status-warn/15 text-foreground",
  bad: "bg-status-bad/15 text-status-bad",
  idle: "bg-muted text-muted-foreground",
};

const APP: Record<number, StatusInfo> = {
  [AppStatusCode.Active]: { tone: "ok", label: "Running" },
  [AppStatusCode.Idle]: { tone: "warn", label: "Idle" },
  [AppStatusCode.Restarting]: { tone: "warn", label: "Restarting", pulse: true },
  [AppStatusCode.Problematic]: { tone: "bad", label: "Problematic" },
  [AppStatusCode.Stopped]: { tone: "idle", label: "Stopped" },
  [AppStatusCode.Unknown]: { tone: "idle", label: "Unknown" },
};

// appStatus maps a container status code to its tone and word. A missing
// code is Unknown: the UI never infers "down" from missing data.
export function appStatus(code: number | undefined): StatusInfo {
  return APP[code ?? AppStatusCode.Unknown] ?? APP[AppStatusCode.Unknown];
}

// serverStatus says whether a server's agent is connected: live liveness
// first, then whether it ever reported in. A standalone install's server
// is the process itself.
export function serverStatus({
  liveness,
  lastSeenAt,
  standalone,
}: {
  liveness?: ServerLiveness;
  lastSeenAt: string | null;
  standalone: boolean;
}): StatusInfo {
  if (standalone) return { tone: "ok", label: "This server" };
  if (liveness === "online") return { tone: "ok", label: "Online" };
  if (!lastSeenAt) return { tone: "idle", label: "Never connected" };
  return { tone: "bad", label: "Offline" };
}

const BACKUP_TONE: Record<BackupState, Omit<StatusInfo, "label">> = {
  ok: { tone: "ok" },
  running: { tone: "ok", pulse: true },
  never: { tone: "idle" },
  off: { tone: "idle" },
  late: { tone: "warn" },
  failing: { tone: "bad" },
};

// backupStatus maps a backup's health state to its tone and word.
export function backupStatus(state: BackupState): StatusInfo {
  return { ...BACKUP_TONE[state], label: STATE_LABELS[state] };
}
```

Create `web/src/components/status.tsx`:

```tsx
import { TONE_DOT_CLASS, TONE_PILL_CLASS, type StatusInfo, type StatusTone } from "@/lib/status";
import { cn } from "@/lib/utils";

// StatusDot is a state's coloured dot. With a label it is an image named by
// the label (for a dot standing alone); without one it is decoration next
// to a word that says the same.
export function StatusDot({
  tone,
  pulse = false,
  label,
  className,
}: {
  tone: StatusTone;
  pulse?: boolean;
  label?: string;
  className?: string;
}) {
  return (
    <span
      data-tone={tone}
      className={cn(
        "inline-block size-2 shrink-0 rounded-full",
        TONE_DOT_CLASS[tone],
        pulse && "animate-pulse motion-reduce:animate-none",
        className,
      )}
      {...(label ? { role: "img", "aria-label": label, title: label } : { "aria-hidden": true })}
    />
  );
}

// StatusPill is a state as a small tinted pill: dot and word.
export function StatusPill({ status, className }: { status: StatusInfo; className?: string }) {
  return (
    <span
      data-tone={status.tone}
      className={cn(
        "inline-flex items-center gap-1.5 whitespace-nowrap rounded-full px-2.5 py-0.5 text-xs font-medium",
        TONE_PILL_CLASS[status.tone],
        className,
      )}
    >
      <StatusDot tone={status.tone} pulse={status.pulse} />
      {status.label}
    </span>
  );
}
```

Replace the whole of `web/src/components/app-status-badge.tsx` with:

```tsx
import { AppStatusCode } from "@/context/apps-context-base";
import { appStatus, TONE_DOT_CLASS } from "@/lib/status";
import { cn } from "@/lib/utils";

// DotLabel is a coloured dot plus a label — the quiet inline status form
// shared by app and volume status.
export function DotLabel({
  label,
  dotClassName,
  className,
}: {
  label: string;
  dotClassName: string;
  className?: string;
}) {
  return (
    <span className={cn("inline-flex items-center gap-1.5", className)}>
      <span className={cn("size-2 shrink-0 rounded-full", dotClassName)} aria-hidden />
      {label}
    </span>
  );
}

// AppStatusIndicator is an app's status as a coloured dot plus the label,
// quiet enough for headers and lists.
export function AppStatusIndicator({ status }: { status?: number }) {
  const info = appStatus(status);
  return (
    <DotLabel
      label={info.label}
      dotClassName={cn(TONE_DOT_CLASS[info.tone], info.pulse && "animate-pulse motion-reduce:animate-none")}
      className={cn(
        "shrink-0 text-sm",
        status === AppStatusCode.Problematic ? "font-medium text-status-bad" : "text-foreground",
      )}
    />
  );
}
```

In `web/src/pages/settings-volumes.tsx`: change `import { StatusDot } from "@/components/app-status-badge";` to `import { DotLabel } from "@/components/app-status-badge";`, and in `VolumeStatus` replace the dot classes and the component:

```tsx
  const [label, dot, text] = volume.in_use
    ? ["In use", "bg-status-ok", ""]
    : volume.orphan
      ? ["Orphaned", "bg-status-warn", "text-amber-700 dark:text-amber-400"]
      : ["Unused", "bg-status-idle", "text-muted-foreground"];
  return (
    <DotLabel
      label={label}
      dotClassName={dot}
      className={cn("whitespace-nowrap", text)}
    />
  );
```

In `web/src/components/template-instances.test.tsx` line 106 change `.querySelector(".bg-emerald-500")` to `.querySelector(".bg-status-ok")`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/status.test.ts src/components/status.test.tsx src/components/template-instances.test.tsx src/pages/settings-volumes.test.tsx src/pages/app-details.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/index.css web/src/lib/status.ts web/src/lib/status.test.ts web/src/components/status.tsx web/src/components/status.test.tsx web/src/components/app-status-badge.tsx web/src/pages/settings-volumes.tsx web/src/components/template-instances.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): status tones as theme tokens, StatusDot and StatusPill

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Page title, PageHeader, Section/RowList, EmptyState, Disclosure

**Files:**
- Create: `web/src/layouts/page-title-base.ts`, `web/src/layouts/page-title.tsx`, `web/src/layouts/use-page-title.ts`, `web/src/layouts/use-page-title.test.tsx`
- Create: `web/src/components/page-header.tsx`, `web/src/components/page-header.test.tsx`
- Create: `web/src/components/section.tsx`, `web/src/components/section.test.tsx`
- Create: `web/src/components/empty-state.tsx`, `web/src/components/empty-state.test.tsx`
- Create: `web/src/components/disclosure.tsx`, `web/src/components/disclosure.test.tsx`

**Decision (page titles):** the outlet-context breadcrumbs (`useAppBreadcrumbs` / `setBreadcrumbs`) are replaced by a page-title context: a page calls `usePageTitle(title)` (directly, or through `PageHeader`), the shell's mobile top bar reads `useCurrentPageTitle()`, and `document.title` becomes `"<title> · WinterFlow"`. A context (not the outlet context) means nested outlets (`ServerScope`, `SettingsLayout`) need no forwarding, and outside the provider (page tests) the hook is a no-op except for `document.title`, so tests need no mock. Task 12 mounts the provider and migrates every caller.

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `PageTitleProvider({ children })` (`@/layouts/page-title`)
  - `usePageTitle(title: string): void`, `useCurrentPageTitle(): string` (`@/layouts/use-page-title`); `APP_NAME = "WinterFlow"` (`@/layouts/page-title-base`)
  - `PageHeader({ title: string; subtitle?: ReactNode; back?: { to: string; label: string }; actions?: ReactNode; className?: string })` — calls `usePageTitle(title)`
  - `Section({ title: string; description?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string })`, `RowList({ children; label?: string; className? })`, `Row({ title: ReactNode; leading?; description?; meta?; actions?; to?: string; className? })` (`@/components/section`)
  - `EmptyState({ title: string; icon?: LucideIcon; action?: ReactNode; className? })`
  - `Disclosure({ label?: string = "More options"; children: ReactNode; defaultOpen?: boolean; open?: boolean; onOpenChange?: (open: boolean) => void; className? })`

- [ ] **Step 1: Write the failing tests**

Create `web/src/layouts/use-page-title.test.tsx`:

```tsx
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";

import { PageTitleProvider } from "@/layouts/page-title";
import { useCurrentPageTitle, usePageTitle } from "@/layouts/use-page-title";

function Page({ title }: { title: string }) {
  usePageTitle(title);
  return null;
}

function Bar() {
  return <p data-testid="bar">{useCurrentPageTitle()}</p>;
}

function Switcher() {
  const [page, setPage] = useState<"a" | "b" | "none">("a");
  return (
    <>
      {page === "a" ? <Page title="Apps" /> : page === "b" ? <Page title="Monitoring" /> : null}
      <button onClick={() => setPage("b")}>b</button>
      <button onClick={() => setPage("none")}>none</button>
    </>
  );
}

describe("usePageTitle", () => {
  afterEach(() => {
    document.title = "";
  });

  it("names the page in the shell and in the browser tab", async () => {
    render(
      <PageTitleProvider>
        <Page title="Members" />
        <Bar />
      </PageTitleProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("bar")).toHaveTextContent("Members"));
    expect(document.title).toBe("Members · WinterFlow");
  });

  it("follows the page on navigation and clears when no page names itself", async () => {
    render(
      <PageTitleProvider>
        <Switcher />
        <Bar />
      </PageTitleProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("bar")).toHaveTextContent("Apps"));
    fireEvent.click(screen.getByRole("button", { name: "b" }));
    await waitFor(() => expect(screen.getByTestId("bar")).toHaveTextContent("Monitoring"));
    fireEvent.click(screen.getByRole("button", { name: "none" }));
    await waitFor(() => expect(screen.getByTestId("bar")).toBeEmptyDOMElement());
  });

  it("still names the browser tab outside the shell", () => {
    render(<Page title="Inbox" />);
    expect(document.title).toBe("Inbox · WinterFlow");
  });
});
```

Create `web/src/components/page-header.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it } from "vitest";

import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";

describe("PageHeader", () => {
  afterEach(() => {
    document.title = "";
  });

  it("is the page's title, subtitle and actions", () => {
    render(
      <MemoryRouter>
        <PageHeader title="Apps" subtitle="homelab · 3 running" actions={<Button>New app</Button>} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { level: 1, name: "Apps" })).toHaveClass("text-2xl", "font-semibold");
    expect(screen.getByText("homelab · 3 running")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New app" })).toBeInTheDocument();
    expect(document.title).toBe("Apps · WinterFlow");
  });

  it("offers a back link on nested pages", () => {
    render(
      <MemoryRouter>
        <PageHeader title="Website" back={{ to: "/templates", label: "Templates" }} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("link", { name: "Templates" })).toHaveAttribute("href", "/templates");
  });

  it("stacks the actions under the title on a phone", () => {
    render(
      <MemoryRouter>
        <PageHeader title="Apps" actions={<Button>New app</Button>} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("banner")).toHaveClass("flex-col", "sm:flex-row");
  });
});
```

Create `web/src/components/section.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { Row, RowList, Section } from "@/components/section";
import { Button } from "@/components/ui/button";

describe("Section", () => {
  it("is a region named by its 15px title, with actions beside it", () => {
    render(
      <Section title="Registries" description="Where images come from" actions={<Button>Add registry</Button>}>
        <p>body</p>
      </Section>,
    );
    const region = screen.getByRole("region", { name: "Registries" });
    expect(within(region).getByRole("heading", { level: 2, name: "Registries" })).toHaveClass("text-[15px]", "font-semibold");
    expect(within(region).getByText("Where images come from")).toBeInTheDocument();
    expect(within(region).getByRole("button", { name: "Add registry" })).toBeInTheDocument();
    expect(within(region).getByText("body")).toBeInTheDocument();
  });
});

describe("RowList", () => {
  it("lists flat rows that become stacked cards on a phone", () => {
    render(
      <MemoryRouter>
        <RowList label="Domains">
          <Row title="example.com" description="Automatic HTTPS" meta="2 apps" to="/x" actions={<Button>Edit</Button>} />
          <Row title="b.example.com" />
        </RowList>
      </MemoryRouter>,
    );
    const list = screen.getByRole("list", { name: "Domains" });
    expect(list).toHaveClass("divide-y", "max-sm:divide-y-0");
    const rows = within(list).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveClass("flex-col", "sm:flex-row", "max-sm:rounded-xl");
    expect(within(rows[0]).getByRole("link", { name: "example.com" })).toHaveAttribute("href", "/x");
    expect(within(rows[0]).getByText("Automatic HTTPS")).toBeInTheDocument();
    expect(within(rows[0]).getByText("2 apps")).toBeInTheDocument();
    expect(within(rows[0]).getByRole("button", { name: "Edit" })).toBeInTheDocument();
    expect(within(rows[1]).queryByRole("link")).not.toBeInTheDocument();
  });
});
```

Create `web/src/components/empty-state.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { Rocket } from "lucide-react";
import { describe, expect, it } from "vitest";

import { EmptyState } from "@/components/empty-state";
import { Button } from "@/components/ui/button";

describe("EmptyState", () => {
  it("says one sentence and offers one action", () => {
    render(<EmptyState icon={Rocket} title="Run your first app." action={<Button>New app</Button>} />);
    expect(screen.getByText("Run your first app.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New app" })).toBeInTheDocument();
  });

  it("can be just the sentence", () => {
    render(<EmptyState title="All quiet." />);
    expect(screen.getByText("All quiet.")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
```

Create `web/src/components/disclosure.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { Disclosure } from "@/components/disclosure";

describe("Disclosure", () => {
  it("hides its fields behind More options until opened", async () => {
    const user = userEvent.setup();
    render(
      <Disclosure>
        <label>
          Region <input />
        </label>
      </Disclosure>,
    );
    const toggle = screen.getByRole("button", { name: "More options" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByLabelText("Region")).not.toBeInTheDocument();
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByLabelText("Region")).toBeInTheDocument();
  });

  it("takes another label and can start open", () => {
    render(
      <Disclosure label="Advanced" defaultOpen>
        <p>retention</p>
      </Disclosure>,
    );
    expect(screen.getByRole("button", { name: "Advanced" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("retention")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/layouts/use-page-title.test.tsx src/components/page-header.test.tsx src/components/section.test.tsx src/components/empty-state.test.tsx src/components/disclosure.test.tsx`
Expected: FAIL — the modules do not exist.

- [ ] **Step 3: Implement**

Create `web/src/layouts/page-title-base.ts`:

```ts
import { createContext } from "react";

// The product name the browser tab shows after the page's title.
export const APP_NAME = "WinterFlow";

// PageTitleContext carries the current page's title (the mobile top bar
// shows it); SetPageTitleContext the setter pages call through
// usePageTitle. Both are absent outside the app shell.
export const PageTitleContext = createContext<string>("");
export const SetPageTitleContext = createContext<((title: string) => void) | null>(null);
```

Create `web/src/layouts/page-title.tsx`:

```tsx
import { useState, type ReactNode } from "react";

import { PageTitleContext, SetPageTitleContext } from "@/layouts/page-title-base";

// PageTitleProvider holds the title the current page set with usePageTitle.
export function PageTitleProvider({ children }: { children: ReactNode }) {
  const [title, setTitle] = useState("");
  return (
    <SetPageTitleContext.Provider value={setTitle}>
      <PageTitleContext.Provider value={title}>{children}</PageTitleContext.Provider>
    </SetPageTitleContext.Provider>
  );
}
```

Create `web/src/layouts/use-page-title.ts`:

```ts
import { useContext, useEffect } from "react";

import { APP_NAME, PageTitleContext, SetPageTitleContext } from "@/layouts/page-title-base";

// usePageTitle names the current page: the shell's mobile top bar shows it
// and the browser tab reads "<title> · WinterFlow". The page that unmounts
// clears it, so a page that names nothing never shows the last one's name.
export function usePageTitle(title: string): void {
  const setTitle = useContext(SetPageTitleContext);
  useEffect(() => {
    setTitle?.(title);
    const previous = document.title;
    document.title = title ? `${title} · ${APP_NAME}` : APP_NAME;
    return () => {
      setTitle?.("");
      document.title = previous;
    };
  }, [setTitle, title]);
}

// useCurrentPageTitle is the title the current page set ("" when none).
export function useCurrentPageTitle(): string {
  return useContext(PageTitleContext);
}
```

Create `web/src/components/page-header.tsx`:

```tsx
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ArrowLeft } from "lucide-react";

import { usePageTitle } from "@/layouts/use-page-title";
import { cn } from "@/lib/utils";

export type PageHeaderProps = {
  title: string;
  subtitle?: ReactNode;
  // back is the page this one sits under ("← Website", "← Templates").
  back?: { to: string; label: string };
  // actions sit top-right on a desktop (one filled primary at most), under
  // the title on a phone.
  actions?: ReactNode;
  className?: string;
};

// PageHeader starts every page: an optional back link, the title (24/600),
// a subtitle and the actions. It also names the page (usePageTitle).
export function PageHeader({ title, subtitle, back, actions, className }: PageHeaderProps) {
  usePageTitle(title);
  return (
    <header className={cn("flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between", className)}>
      <div className="min-w-0 space-y-1">
        {back ? (
          <Link
            to={back.to}
            className="inline-flex min-h-8 items-center gap-1 text-sm text-muted-foreground transition-colors duration-150 hover:text-foreground"
          >
            <ArrowLeft className="size-4" aria-hidden />
            {back.label}
          </Link>
        ) : null}
        <h1 className="truncate text-2xl font-semibold tracking-tight">{title}</h1>
        {subtitle ? <p className="text-sm text-muted-foreground">{subtitle}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  );
}
```

Create `web/src/components/section.tsx`:

```tsx
import { useId, type ReactNode } from "react";
import { Link } from "react-router-dom";

import { cn } from "@/lib/utils";

// Section is a titled part of a page (15/600 title, optional description
// and actions) — a region named by its title.
export function Section({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  const id = useId();
  return (
    <section aria-labelledby={id} className={cn("space-y-3", className)}>
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div className="min-w-0 space-y-0.5">
          <h2 id={id} className="text-[15px] font-semibold leading-6">
            {title}
          </h2>
          {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
      {children}
    </section>
  );
}

// RowList is a flat list of rows in one soft frame; on a phone the frame
// goes and every row is its own card.
export function RowList({ children, label, className }: { children: ReactNode; label?: string; className?: string }) {
  return (
    <ul
      aria-label={label}
      className={cn(
        "divide-y divide-border/70 overflow-hidden rounded-xl border bg-card",
        "max-sm:space-y-2 max-sm:divide-y-0 max-sm:overflow-visible max-sm:rounded-none max-sm:border-0 max-sm:bg-transparent",
        className,
      )}
    >
      {children}
    </ul>
  );
}

// Row is one item of a RowList: leading visual, title (a link covering the
// row when `to` is set), description, meta and actions (above the link).
export function Row({
  title,
  leading,
  description,
  meta,
  actions,
  to,
  className,
}: {
  title: ReactNode;
  leading?: ReactNode;
  description?: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
  to?: string;
  className?: string;
}) {
  return (
    <li
      className={cn(
        "relative flex flex-col gap-2 px-4 py-3 transition-colors duration-150 hover:bg-muted/40 sm:flex-row sm:items-center sm:gap-4",
        "max-sm:rounded-xl max-sm:border max-sm:bg-card",
        className,
      )}
    >
      <div className="flex min-w-0 flex-1 items-center gap-3">
        {leading ? <div className="shrink-0">{leading}</div> : null}
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">
            {to ? (
              <Link to={to} className="after:absolute after:inset-0 focus-visible:outline-none focus-visible:underline">
                {title}
              </Link>
            ) : (
              title
            )}
          </div>
          {description ? <div className="truncate text-xs text-muted-foreground">{description}</div> : null}
        </div>
      </div>
      {meta ? <div className="text-xs text-muted-foreground sm:text-right">{meta}</div> : null}
      {actions ? <div className="relative z-10 flex items-center gap-1">{actions}</div> : null}
    </li>
  );
}
```

Create `web/src/components/empty-state.tsx`:

```tsx
import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";

import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia } from "@/components/ui/empty";
import { cn } from "@/lib/utils";

// EmptyState stands in for a list with nothing in it: an icon, one
// sentence, at most one action.
export function EmptyState({
  title,
  icon: Icon,
  action,
  className,
}: {
  title: string;
  icon?: LucideIcon;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <Empty className={cn("rounded-xl border border-dashed", className)}>
      <EmptyHeader>
        {Icon ? (
          <EmptyMedia variant="icon">
            <Icon />
          </EmptyMedia>
        ) : null}
        <EmptyDescription className="text-sm text-foreground">{title}</EmptyDescription>
      </EmptyHeader>
      {action ? <EmptyContent>{action}</EmptyContent> : null}
    </Empty>
  );
}
```

Create `web/src/components/disclosure.tsx`:

```tsx
import type { ReactNode } from "react";
import { ChevronRight } from "lucide-react";

import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";

// Disclosure keeps optional fields out of the way ("More options",
// "Advanced"): one styled toggle, the fields below it once opened.
export function Disclosure({
  label = "More options",
  children,
  defaultOpen = false,
  open,
  onOpenChange,
  className,
}: {
  label?: string;
  children: ReactNode;
  defaultOpen?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  className?: string;
}) {
  return (
    <Collapsible
      defaultOpen={defaultOpen}
      open={open}
      onOpenChange={onOpenChange}
      className={cn("space-y-3", className)}
    >
      <CollapsibleTrigger className="group inline-flex min-h-10 items-center gap-1.5 rounded-md text-sm font-medium text-muted-foreground transition-colors duration-150 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronRight
          className="size-4 transition-transform duration-150 group-data-[state=open]:rotate-90 motion-reduce:transition-none"
          aria-hidden
        />
        {label}
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-4">{children}</CollapsibleContent>
    </Collapsible>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/layouts/use-page-title.test.tsx src/components/page-header.test.tsx src/components/section.test.tsx src/components/empty-state.test.tsx src/components/disclosure.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/layouts/page-title-base.ts web/src/layouts/page-title.tsx web/src/layouts/use-page-title.ts web/src/layouts/use-page-title.test.tsx web/src/components/page-header.tsx web/src/components/page-header.test.tsx web/src/components/section.tsx web/src/components/section.test.tsx web/src/components/empty-state.tsx web/src/components/empty-state.test.tsx web/src/components/disclosure.tsx web/src/components/disclosure.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): page title context, PageHeader, Section/RowList, EmptyState, Disclosure

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Media queries, ConfirmDialog and ResponsiveDialog

**Files:**
- Create: `web/src/hooks/use-media-query.ts`, `web/src/hooks/use-media-query.test.tsx`
- Modify: `web/src/hooks/use-mobile.ts` (1024px, answers on the first render)
- Create: `web/src/test/media.ts`
- Create: `web/src/components/confirm-dialog.tsx`, `web/src/components/confirm-dialog.test.tsx`
- Create: `web/src/components/responsive-dialog.tsx`, `web/src/components/responsive-dialog.test.tsx`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `useMediaQuery(query: string): boolean` — `false` without `matchMedia` (jsdom), correct on the first render (`useSyncExternalStore`)
  - `useIsMobile(): boolean` (true below 1024px), `MOBILE_BREAKPOINT = 1024` (`@/hooks/use-mobile`; the vendored sidebar already imports `useIsMobile`, so its sheet now opens below 1024px)
  - `stubViewport(width: number, opts?: { dark?: boolean }): void` (`@/test/media`; undo with `vi.unstubAllGlobals()`)
  - `ConfirmDialog(props: ConfirmDialogProps)` with `ConfirmDialogProps = { open: boolean; onOpenChange: (open: boolean) => void; title: string; description?: ReactNode; confirmLabel: string; pendingLabel?: string; cancelLabel?: string; destructive?: boolean /* default true */; onConfirm: () => void | Promise<void>; children?: ReactNode }` — closes when `onConfirm` resolves, stays open (re-enabled) when it rejects; the caller reports the error.
  - `ResponsiveDialog(props: ResponsiveDialogProps)` with `ResponsiveDialogProps = { open: boolean; onOpenChange: (open: boolean) => void; title: ReactNode; description?: ReactNode; children: ReactNode; footer?: ReactNode; className?: string }`; `PHONE_QUERY = "(max-width: 639px)"`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/test/media.ts`:

```ts
import { vi } from "vitest";

// stubViewport makes window.matchMedia answer (min-width: Npx) and
// (max-width: Npx) queries for a viewport `width` px wide, and
// prefers-color-scheme: dark when opts.dark is set. jsdom has no
// matchMedia at all (every hook reads that as a desktop). Undo with
// vi.unstubAllGlobals().
export function stubViewport(width: number, opts: { dark?: boolean } = {}): void {
  vi.stubGlobal("matchMedia", (query: string) => {
    const min = /\(min-width:\s*(\d+)px\)/.exec(query);
    const max = /\(max-width:\s*(\d+)px\)/.exec(query);
    let matches = false;
    if (min) matches = width >= Number(min[1]);
    else if (max) matches = width <= Number(max[1]);
    else if (query.includes("prefers-color-scheme: dark")) matches = Boolean(opts.dark);
    return {
      matches,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    };
  });
}
```

Create `web/src/hooks/use-media-query.test.tsx`:

```tsx
import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useMediaQuery } from "@/hooks/use-media-query";
import { useIsMobile } from "@/hooks/use-mobile";
import { stubViewport } from "@/test/media";

// seen records every render's answer: the first one must already be right.
const seen = vi.fn();
function Probe() {
  seen(useIsMobile());
  return null;
}

function Query({ q }: { q: string }) {
  return <p data-testid="match">{String(useMediaQuery(q))}</p>;
}

describe("useIsMobile", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    seen.mockClear();
  });

  it("is mobile below 1024px, on the very first render", () => {
    stubViewport(375);
    render(<Probe />);
    expect(seen.mock.calls[0][0]).toBe(true);
  });

  it("is a desktop at 1024px and up", () => {
    stubViewport(1024);
    render(<Probe />);
    expect(seen.mock.calls[0][0]).toBe(false);
  });

  it("reads a browser without matchMedia (jsdom) as a desktop", () => {
    render(<Probe />);
    expect(seen.mock.calls[0][0]).toBe(false);
  });
});

describe("useMediaQuery", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("re-renders when the query starts matching", () => {
    const listeners = new Set<() => void>();
    const mql = {
      matches: false,
      addEventListener: (_: string, l: () => void) => listeners.add(l),
      removeEventListener: (_: string, l: () => void) => listeners.delete(l),
    };
    vi.stubGlobal("matchMedia", () => mql);
    const { unmount } = render(<Query q="(max-width: 639px)" />);
    expect(screen.getByTestId("match")).toHaveTextContent("false");
    act(() => {
      mql.matches = true;
      listeners.forEach((l) => l());
    });
    expect(screen.getByTestId("match")).toHaveTextContent("true");
    unmount();
    expect(listeners.size).toBe(0);
  });
});
```

Create `web/src/components/confirm-dialog.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { ConfirmDialog } from "@/components/confirm-dialog";

function Harness({ onConfirm }: { onConfirm: () => void | Promise<void> }) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <p>{open ? "open" : "closed"}</p>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="Remove Bob?"
        description="Their account and tokens are deleted."
        confirmLabel="Remove"
        pendingLabel="Removing…"
        onConfirm={onConfirm}
      />
    </>
  );
}

describe("ConfirmDialog", () => {
  it("asks with a title, a body and the action's name", () => {
    render(<Harness onConfirm={vi.fn()} />);
    const dialog = screen.getByRole("alertdialog", { name: "Remove Bob?" });
    expect(dialog).toHaveTextContent("Their account and tokens are deleted.");
    expect(screen.getByRole("button", { name: "Remove" })).toHaveClass("bg-destructive");
  });

  it("closes on Cancel without acting", async () => {
    const onConfirm = vi.fn();
    render(<Harness onConfirm={onConfirm} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByText("closed")).toBeInTheDocument();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("shows the action pending, then closes when it is done", async () => {
    let finish: () => void = () => {};
    const onConfirm = vi.fn(() => new Promise<void>((resolve) => (finish = resolve)));
    render(<Harness onConfirm={onConfirm} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "Remove" }));
    expect(await screen.findByRole("button", { name: "Removing…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    finish();
    expect(await screen.findByText("closed")).toBeInTheDocument();
  });

  it("stays open to retry when the action fails", async () => {
    const onConfirm = vi.fn(() => Promise.reject(new Error("busy")));
    render(<Harness onConfirm={onConfirm} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "Remove" }));
    expect(await screen.findByRole("button", { name: "Remove" })).toBeEnabled();
    expect(screen.getByText("open")).toBeInTheDocument();
  });
});
```

Create `web/src/components/responsive-dialog.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { stubViewport } from "@/test/media";

function renderDialog() {
  return render(
    <ResponsiveDialog
      open
      onOpenChange={() => {}}
      title="Add a domain"
      description="Covers its subdomains too."
      footer={<Button>Add</Button>}
    >
      <label>
        Name <input />
      </label>
    </ResponsiveDialog>,
  );
}

describe("ResponsiveDialog", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("is a centred dialog on a wider screen", () => {
    renderDialog();
    const dialog = screen.getByRole("dialog", { name: "Add a domain" });
    expect(dialog).toHaveClass("sm:rounded-lg");
    expect(dialog).toHaveTextContent("Covers its subdomains too.");
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add" })).toBeInTheDocument();
  });

  it("is a bottom sheet on a phone", () => {
    stubViewport(375);
    renderDialog();
    const sheet = screen.getByRole("dialog", { name: "Add a domain" });
    expect(sheet).toHaveClass("rounded-t-[10px]");
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add" })).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/hooks/use-media-query.test.tsx src/components/confirm-dialog.test.tsx src/components/responsive-dialog.test.tsx`
Expected: FAIL — `@/hooks/use-media-query`, `@/components/confirm-dialog`, `@/components/responsive-dialog` do not exist (and `useIsMobile` reads `false` on the first render at 375px).

- [ ] **Step 3: Implement**

Create `web/src/hooks/use-media-query.ts`:

```ts
import { useCallback, useSyncExternalStore } from "react";

function hasMatchMedia(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function";
}

// useMediaQuery reports whether a CSS media query matches, right from the
// first render (a layout that redirects or picks a dialog form must not see
// a wrong first answer), and re-renders when that changes. Without
// matchMedia (jsdom) nothing matches: tests are desktops by default.
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      if (!hasMatchMedia()) return () => {};
      const mql = window.matchMedia(query);
      mql.addEventListener("change", onChange);
      return () => mql.removeEventListener("change", onChange);
    },
    [query],
  );
  const snapshot = () => hasMatchMedia() && window.matchMedia(query).matches;
  return useSyncExternalStore(subscribe, snapshot, () => false);
}
```

Replace the whole of `web/src/hooks/use-mobile.ts` with:

```ts
import { useMediaQuery } from "@/hooks/use-media-query"

// Below this width the shell is the phone/tablet one: the sidebar is a
// sheet behind a slim top bar (the vendored sidebar reads useIsMobile).
export const MOBILE_BREAKPOINT = 1024

export function useIsMobile(): boolean {
  return useMediaQuery(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)
}
```

Create `web/src/components/confirm-dialog.tsx`:

```tsx
import { useState, type ReactNode } from "react";

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

export type ConfirmDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: ReactNode;
  // confirmLabel names the action ("Remove", "Delete app"), never "OK".
  confirmLabel: string;
  pendingLabel?: string;
  cancelLabel?: string;
  // destructive (the default) paints the action red.
  destructive?: boolean;
  // onConfirm runs the action. The dialog shows it pending, closes when it
  // resolves and stays open (to retry or cancel) when it rejects; the
  // action reports its own failure (a toast).
  onConfirm: () => void | Promise<void>;
  // children is extra body (choices such as "Also delete the volumes").
  children?: ReactNode;
};

// ConfirmDialog is the one destructive-confirm of the app.
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  pendingLabel,
  cancelLabel = "Cancel",
  destructive = true,
  onConfirm,
  children,
}: ConfirmDialogProps) {
  const [pending, setPending] = useState(false);

  const confirm = async () => {
    setPending(true);
    try {
      await onConfirm();
      onOpenChange(false);
    } catch {
      // onConfirm reported the failure; keep the dialog for a retry.
    } finally {
      setPending(false);
    }
  };

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!pending) onOpenChange(next);
      }}
    >
      <AlertDialogContent {...(description ? {} : { "aria-describedby": undefined })}>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description ? <AlertDialogDescription>{description}</AlertDialogDescription> : null}
        </AlertDialogHeader>
        {children}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>{cancelLabel}</AlertDialogCancel>
          <Button variant={destructive ? "destructive" : "default"} disabled={pending} onClick={() => void confirm()}>
            {/* The spinner is decoration: the label already says it is pending. */}
            {pending ? <Spinner className="size-4" aria-hidden /> : null}
            {pending ? (pendingLabel ?? confirmLabel) : confirmLabel}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
```

Create `web/src/components/responsive-dialog.tsx`:

```tsx
import type { ReactNode } from "react";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerFooter,
  DrawerHeader,
  DrawerTitle,
} from "@/components/ui/drawer";
import { useMediaQuery } from "@/hooks/use-media-query";
import { cn } from "@/lib/utils";

// Phones (below 640px) get a bottom sheet instead of a centred dialog.
export const PHONE_QUERY = "(max-width: 639px)";

export type ResponsiveDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  // footer holds the actions (Cancel, then the primary).
  footer?: ReactNode;
  className?: string;
};

// ResponsiveDialog is the standard dialog: shadcn's Dialog on wider
// screens, a vaul bottom sheet on phones, with the same title, body and
// footer.
export function ResponsiveDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  className,
}: ResponsiveDialogProps) {
  const phone = useMediaQuery(PHONE_QUERY);
  const describedBy = description ? {} : { "aria-describedby": undefined };

  if (phone) {
    return (
      <Drawer open={open} onOpenChange={onOpenChange} shouldScaleBackground={false}>
        <DrawerContent className={cn("max-h-[90svh]", className)} {...describedBy}>
          <DrawerHeader className="text-left">
            <DrawerTitle>{title}</DrawerTitle>
            {description ? <DrawerDescription>{description}</DrawerDescription> : null}
          </DrawerHeader>
          <div className="overflow-y-auto px-4 pb-2">{children}</div>
          {footer ? <DrawerFooter className="pb-[max(1rem,env(safe-area-inset-bottom))]">{footer}</DrawerFooter> : null}
        </DrawerContent>
      </Drawer>
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className={cn("max-h-[90svh] overflow-y-auto", className)} {...describedBy}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>
        {children}
        {footer ? <DialogFooter>{footer}</DialogFooter> : null}
      </DialogContent>
    </Dialog>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/hooks/use-media-query.test.tsx src/components/confirm-dialog.test.tsx src/components/responsive-dialog.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, run the whole suite once (the sidebar breakpoint moved), commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b && pnpm --dir web exec vitest run
git add web/src/hooks/use-media-query.ts web/src/hooks/use-media-query.test.tsx web/src/hooks/use-mobile.ts web/src/test/media.ts web/src/components/confirm-dialog.tsx web/src/components/confirm-dialog.test.tsx web/src/components/responsive-dialog.tsx web/src/components/responsive-dialog.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): ConfirmDialog, ResponsiveDialog and a 1024px mobile breakpoint

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 5: Route helpers and the Settings sub-menu model

**Files:**
- Create: `web/src/lib/routes.ts`, `web/src/lib/routes.test.ts`
- Create: `web/src/lib/settings-nav.ts`, `web/src/lib/settings-nav.test.ts`

**Interfaces:**
- Consumes: `serverPath(serverId, sub)` (`@/lib/server-paths`).
- Produces (`@/lib/routes`):
  - types `NewAppSource = "template" | "compose" | "git"`, `AppTab = "overview" | "logs" | "configuration" | "backups" | "metrics" | "settings"`, `ServerSettingsSection = "general" | "domains" | "storages" | "docker"`, `OrgSettingsSection = "organization" | "members" | "variables" | "notifications" | "password" | "tokens"`
  - constants `INBOX_PATH = "/inbox"`, `TEMPLATES_PATH = "/templates"`, `NEW_TEMPLATE_PATH = "/templates/new"`, `SETTINGS_PATH = "/settings"`
  - `appsPath(serverId)`, `newAppSub(from?)`, `newAppPath(serverId, from?)`, `appSub(appId, tab?)`, `appPath(serverId, appId, tab?)`, `monitoringPath(serverId)`, `serverSettingsSub(section = "general")`, `serverSettingsPath(serverId, section = "general")`, `settingsPath(section: OrgSettingsSection)`, `templatePath(templateId)` — all `string`
  - `LEGACY_APP_TABS: { readonly editor: "configuration" }` (phase 2 adds `history`)
  - `legacyServerSettingsTarget(serverId: string, tab: string | null): string`, `legacyAlertsTarget(tab: string | null): string`, `withQuery(target: string, search: string, hash: string): string`, `isWidePath(pathname: string): boolean`
- Produces (`@/lib/settings-nav`): `type SettingsItem = { label: string; to: string; icon: LucideIcon }`, `type SettingsSection = { title: string; items: SettingsItem[] }`, `settingsSections(opts: { server: { id: string; name: string; hasIngress: boolean } | null; hasPassword: boolean }): SettingsSection[]`

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/routes.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import {
  INBOX_PATH,
  LEGACY_APP_TABS,
  NEW_TEMPLATE_PATH,
  SETTINGS_PATH,
  TEMPLATES_PATH,
  appPath,
  appSub,
  appsPath,
  isWidePath,
  legacyAlertsTarget,
  legacyServerSettingsTarget,
  monitoringPath,
  newAppPath,
  newAppSub,
  serverSettingsPath,
  serverSettingsSub,
  settingsPath,
  templatePath,
  withQuery,
} from "@/lib/routes";

describe("route builders", () => {
  it("builds the server-scoped pages", () => {
    expect(appsPath("s1")).toBe("/server/s1");
    expect(newAppPath("s1")).toBe("/server/s1/new");
    expect(newAppPath("s1", "template")).toBe("/server/s1/new?from=template");
    expect(newAppSub("git")).toBe("/new?from=git");
    expect(appPath("s1", "a1")).toBe("/server/s1/app/a1");
    expect(appPath("s1", "a1", "metrics")).toBe("/server/s1/app/a1?tab=metrics");
    expect(appSub("a b")).toBe("/app/a%20b");
    expect(monitoringPath("s1")).toBe("/server/s1/monitoring");
    expect(serverSettingsPath("s1")).toBe("/server/s1/settings/general");
    expect(serverSettingsPath("s1", "docker")).toBe("/server/s1/settings/docker");
    expect(serverSettingsSub("storages")).toBe("/settings/storages");
  });

  it("builds the org-wide pages", () => {
    expect(INBOX_PATH).toBe("/inbox");
    expect(TEMPLATES_PATH).toBe("/templates");
    expect(NEW_TEMPLATE_PATH).toBe("/templates/new");
    expect(SETTINGS_PATH).toBe("/settings");
    expect(templatePath("t/1")).toBe("/templates/t%2F1");
    expect(settingsPath("members")).toBe("/settings/members");
  });
});

describe("legacy URLs", () => {
  it.each([
    [null, "/server/s1/settings/general"],
    ["agent", "/server/s1/settings/general"],
    ["registries", "/server/s1/settings/docker"],
    ["networks", "/server/s1/settings/docker"],
    ["volumes", "/server/s1/settings/docker"],
    ["domains", "/server/s1/settings/domains"],
    ["storages", "/server/s1/settings/storages"],
    ["variables", "/settings/variables"],
    ["nonsense", "/server/s1/settings/general"],
  ])("sends the server settings tab %s to %s", (tab, target) => {
    expect(legacyServerSettingsTarget("s1", tab)).toBe(target);
  });

  it.each([
    [null, "/inbox"],
    ["history", "/inbox"],
    ["rules", "/settings/notifications"],
    ["channels", "/settings/notifications"],
  ])("sends the alerts tab %s to %s", (tab, target) => {
    expect(legacyAlertsTarget(tab)).toBe(target);
  });

  it("renames the app page's Editor tab to Configuration", () => {
    expect(LEGACY_APP_TABS.editor).toBe("configuration");
  });

  it("carries the old query and hash over unless the target has its own query", () => {
    expect(withQuery("/settings/members", "?x=1", "#top")).toBe("/settings/members?x=1#top");
    expect(withQuery("/server/s1/new?from=template", "?x=1", "")).toBe("/server/s1/new?from=template");
    expect(withQuery("/inbox", "", "")).toBe("/inbox");
  });
});

describe("isWidePath", () => {
  it("lets the app page and Monitoring use the full width", () => {
    expect(isWidePath("/server/s1/app/a1")).toBe(true);
    expect(isWidePath("/server/s1/monitoring")).toBe(true);
    expect(isWidePath("/server/s1")).toBe(false);
    expect(isWidePath("/server/s1/settings/general")).toBe(false);
    expect(isWidePath("/settings/members")).toBe(false);
  });
});
```

Create `web/src/lib/settings-nav.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { settingsSections } from "@/lib/settings-nav";

const flat = (sections: ReturnType<typeof settingsSections>) =>
  sections.map((s) => [s.title, s.items.map((i) => [i.label, i.to])]);

describe("settingsSections", () => {
  it("lists the server's sections, then the organization's, then the account's", () => {
    expect(flat(settingsSections({ server: { id: "s1", name: "homelab", hasIngress: true }, hasPassword: true }))).toEqual([
      [
        "Server · homelab",
        [
          ["General", "/server/s1/settings/general"],
          ["Domains", "/server/s1/settings/domains"],
          ["Storages", "/server/s1/settings/storages"],
          ["Docker", "/server/s1/settings/docker"],
        ],
      ],
      [
        "Organization",
        [
          ["General", "/settings/organization"],
          ["Members", "/settings/members"],
          ["Shared variables", "/settings/variables"],
          ["Notifications", "/settings/notifications"],
        ],
      ],
      [
        "Account",
        [
          ["Password", "/settings/password"],
          ["API tokens", "/settings/tokens"],
        ],
      ],
    ]);
  });

  it("offers Domains only on a server with ingress", () => {
    const [server] = settingsSections({ server: { id: "s1", name: "homelab", hasIngress: false }, hasPassword: true });
    expect(server.items.map((i) => i.label)).toEqual(["General", "Storages", "Docker"]);
  });

  it("has no server section without a server, and no Password without local credentials", () => {
    const sections = settingsSections({ server: null, hasPassword: false });
    expect(sections.map((s) => s.title)).toEqual(["Organization", "Account"]);
    expect(sections[1].items.map((i) => i.label)).toEqual(["API tokens"]);
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/routes.test.ts src/lib/settings-nav.test.ts`
Expected: FAIL — modules not found.

- [ ] **Step 3: Implement**

Create `web/src/lib/routes.ts`:

```ts
import { serverPath } from "@/lib/server-paths";

// The route table of the UX rebuild (spec §2 "Routes"): one builder per
// page so links never spell paths by hand, and the mapping of every
// pre-rebuild URL to its new place.

export type NewAppSource = "template" | "compose" | "git";
export type AppTab = "overview" | "logs" | "configuration" | "backups" | "metrics" | "settings";
export type ServerSettingsSection = "general" | "domains" | "storages" | "docker";
export type OrgSettingsSection = "organization" | "members" | "variables" | "notifications" | "password" | "tokens";

export const INBOX_PATH = "/inbox";
export const TEMPLATES_PATH = "/templates";
export const NEW_TEMPLATE_PATH = "/templates/new";
export const SETTINGS_PATH = "/settings";

export function appsPath(serverId: string): string {
  return serverPath(serverId);
}

export function newAppSub(from?: NewAppSource): string {
  return from ? `/new?from=${from}` : "/new";
}

export function newAppPath(serverId: string, from?: NewAppSource): string {
  return serverPath(serverId, newAppSub(from));
}

export function appSub(appId: string, tab?: AppTab): string {
  const base = `/app/${encodeURIComponent(appId)}`;
  return tab ? `${base}?tab=${tab}` : base;
}

export function appPath(serverId: string, appId: string, tab?: AppTab): string {
  return serverPath(serverId, appSub(appId, tab));
}

export function monitoringPath(serverId: string): string {
  return serverPath(serverId, "/monitoring");
}

export function serverSettingsSub(section: ServerSettingsSection = "general"): string {
  return `/settings/${section}`;
}

export function serverSettingsPath(serverId: string, section: ServerSettingsSection = "general"): string {
  return serverPath(serverId, serverSettingsSub(section));
}

export function settingsPath(section: OrgSettingsSection): string {
  return `${SETTINGS_PATH}/${section}`;
}

export function templatePath(templateId: string): string {
  return `${TEMPLATES_PATH}/${encodeURIComponent(templateId)}`;
}

// LEGACY_APP_TABS maps an app page ?tab= value of before the rebuild to
// its new name (the URL is rewritten in place).
export const LEGACY_APP_TABS = { editor: "configuration" } as const satisfies Record<string, AppTab>;

// legacyServerSettingsTarget is where /server/:id/settings?tab=… went:
// each tab is a page of Settings → Server now, the server's variables are
// part of Settings → Shared variables, and the agent update is on General.
export function legacyServerSettingsTarget(serverId: string, tab: string | null): string {
  switch (tab) {
    case "variables":
      return settingsPath("variables");
    case "domains":
      return serverSettingsPath(serverId, "domains");
    case "storages":
      return serverSettingsPath(serverId, "storages");
    case "registries":
    case "networks":
    case "volumes":
      return serverSettingsPath(serverId, "docker");
    default:
      return serverSettingsPath(serverId, "general");
  }
}

// legacyAlertsTarget is where /org/alerts?tab=… went: the history is the
// Inbox, rules and channels are Settings → Notifications.
export function legacyAlertsTarget(tab: string | null): string {
  return tab === "rules" || tab === "channels" ? settingsPath("notifications") : INBOX_PATH;
}

// withQuery appends an old URL's query to a redirect target that has none
// of its own, and its hash in any case.
export function withQuery(target: string, search: string, hash: string): string {
  const base = target.includes("?") || !search ? target : `${target}${search}`;
  return `${base}${hash}`;
}

// isWidePath reports the pages that use the full width (the app page with
// its logs and charts, Monitoring); every other page is capped at 1100px.
export function isWidePath(pathname: string): boolean {
  return /^\/server\/[^/]+\/(app\/[^/]+|monitoring)\/?$/.test(pathname);
}
```

Create `web/src/lib/settings-nav.ts`:

```ts
import {
  BellRing,
  Braces,
  Building2,
  Container,
  Globe,
  HardDrive,
  KeyRound,
  LockKeyhole,
  Server,
  Users,
  type LucideIcon,
} from "lucide-react";

import { serverSettingsPath, settingsPath } from "@/lib/routes";

export type SettingsItem = { label: string; to: string; icon: LucideIcon };
export type SettingsSection = { title: string; items: SettingsItem[] };

// settingsSections is the Settings sub-menu (spec §6): the active server's
// sections (Domains only with ingress), the organization's, and the
// account's (Password only for accounts with local credentials). The
// layout and the ⌘K palette both read it.
export function settingsSections({
  server,
  hasPassword,
}: {
  server: { id: string; name: string; hasIngress: boolean } | null;
  hasPassword: boolean;
}): SettingsSection[] {
  const sections: SettingsSection[] = [];
  if (server) {
    const items: SettingsItem[] = [{ label: "General", to: serverSettingsPath(server.id, "general"), icon: Server }];
    if (server.hasIngress) items.push({ label: "Domains", to: serverSettingsPath(server.id, "domains"), icon: Globe });
    items.push(
      { label: "Storages", to: serverSettingsPath(server.id, "storages"), icon: HardDrive },
      { label: "Docker", to: serverSettingsPath(server.id, "docker"), icon: Container },
    );
    sections.push({ title: `Server · ${server.name}`, items });
  }
  sections.push({
    title: "Organization",
    items: [
      { label: "General", to: settingsPath("organization"), icon: Building2 },
      { label: "Members", to: settingsPath("members"), icon: Users },
      { label: "Shared variables", to: settingsPath("variables"), icon: Braces },
      { label: "Notifications", to: settingsPath("notifications"), icon: BellRing },
    ],
  });
  const account: SettingsItem[] = [];
  if (hasPassword) account.push({ label: "Password", to: settingsPath("password"), icon: LockKeyhole });
  account.push({ label: "API tokens", to: settingsPath("tokens"), icon: KeyRound });
  sections.push({ title: "Account", items: account });
  return sections;
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/routes.test.ts src/lib/settings-nav.test.ts`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/routes.ts web/src/lib/routes.test.ts web/src/lib/settings-nav.ts web/src/lib/settings-nav.test.ts
git commit -m "$(cat <<'EOF'
feat(web): route builders, legacy URL mapping and the Settings menu model

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: One tab style synced to `?tab=` (and the app page on it)

**Files:**
- Create: `web/src/components/underline-tabs.tsx`, `web/src/components/underline-tabs.test.tsx`
- Create: `web/src/hooks/use-tab-param.ts`, `web/src/hooks/use-tab-param.test.tsx`
- Modify: `web/src/pages/app-details.tsx` (tabs through the wrapper; `editor` → `configuration`)
- Modify: `web/src/pages/app-details.test.tsx` (new test)

**Interfaces:**
- Consumes: `LEGACY_APP_TABS` (Task 5).
- Produces:
  - `UnderlineTabs<T extends string>({ value: T; onValueChange: (value: T) => void; items: { value: T; label: ReactNode }[]; children: ReactNode; label?: string; className?: string })` — renders Radix `Tabs` + a horizontally scrollable underline `TabsList`; panels are the caller's `TabsContent` (from `@/components/ui/tabs`) passed as children.
  - `useTabParam<T extends string>(tabs: readonly T[], fallback: T, opts?: { param?: string /* "tab" */; aliases?: Readonly<Record<string, T>> }): [T, (next: T) => void]` — reads `?tab=`, maps an alias and rewrites it in the URL (replace), falls back on a missing or unknown value; the setter keeps other query params and drops the param for the fallback tab.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/underline-tabs.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { UnderlineTabs } from "@/components/underline-tabs";
import { TabsContent } from "@/components/ui/tabs";

const ITEMS = [
  { value: "overview", label: "Overview" },
  { value: "logs", label: "Logs" },
] as const;

function renderTabs(value: "overview" | "logs", onValueChange = vi.fn()) {
  return render(
    <UnderlineTabs value={value} onValueChange={onValueChange} items={[...ITEMS]} label="App sections">
      <TabsContent value="overview">overview panel</TabsContent>
      <TabsContent value="logs">logs panel</TabsContent>
    </UnderlineTabs>,
  );
}

describe("UnderlineTabs", () => {
  it("shows the selected tab's panel under an underline tab bar", () => {
    renderTabs("logs");
    expect(screen.getByRole("tablist", { name: "App sections" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Logs" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "Logs" })).toHaveClass("border-b-2", "min-h-10");
    expect(screen.getByText("logs panel")).toBeInTheDocument();
    expect(screen.queryByText("overview panel")).not.toBeInTheDocument();
  });

  it("reports the tab picked", async () => {
    const onValueChange = vi.fn();
    renderTabs("logs", onValueChange);
    await userEvent.setup().click(screen.getByRole("tab", { name: "Overview" }));
    expect(onValueChange).toHaveBeenCalledWith("overview");
  });

  it("scrolls inside itself on a phone instead of widening the page", () => {
    renderTabs("logs");
    const list = screen.getByRole("tablist");
    expect(list).toHaveClass("w-max");
    expect(list.parentElement).toHaveClass("max-w-full", "overflow-x-auto");
  });

  it("brings the selected tab into view", () => {
    const scrolled: Element[] = [];
    const spy = vi.spyOn(Element.prototype, "scrollIntoView").mockImplementation(function (this: Element) {
      scrolled.push(this);
    });
    renderTabs("logs");
    expect(scrolled).toContain(screen.getByRole("tab", { name: "Logs" }));
    spy.mockRestore();
  });
});
```

Create `web/src/hooks/use-tab-param.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { useTabParam } from "@/hooks/use-tab-param";

const TABS = ["overview", "logs", "configuration"] as const;

function Probe() {
  const [tab, setTab] = useTabParam(TABS, "overview", { aliases: { editor: "configuration" } });
  const { search } = useLocation();
  return (
    <div>
      <span data-testid="tab">{tab}</span>
      <span data-testid="search">{search}</span>
      <button onClick={() => setTab("logs")}>logs</button>
      <button onClick={() => setTab("overview")}>overview</button>
    </div>
  );
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Probe />
    </MemoryRouter>,
  );
}

describe("useTabParam", () => {
  it("falls back without a tab or with one it doesn't know", () => {
    renderAt("/app");
    expect(screen.getByTestId("tab")).toHaveTextContent("overview");
  });

  it("reads a known tab", () => {
    renderAt("/app?tab=logs");
    expect(screen.getByTestId("tab")).toHaveTextContent("logs");
  });

  it("ignores an unknown tab", () => {
    renderAt("/app?tab=nope");
    expect(screen.getByTestId("tab")).toHaveTextContent("overview");
  });

  it("opens an old tab name under its new name and rewrites the URL", async () => {
    renderAt("/app?tab=editor&x=1");
    expect(screen.getByTestId("tab")).toHaveTextContent("configuration");
    expect(await screen.findByText("?tab=configuration&x=1")).toBeInTheDocument();
  });

  it("keeps other parameters and leaves the default tab out of the URL", async () => {
    const user = userEvent.setup();
    renderAt("/app?x=1");
    await user.click(screen.getByRole("button", { name: "logs" }));
    expect(screen.getByTestId("search")).toHaveTextContent("?x=1&tab=logs");
    await user.click(screen.getByRole("button", { name: "overview" }));
    expect(screen.getByTestId("search")).toHaveTextContent(/^\?x=1$/);
  });
});
```

Add to `web/src/pages/app-details.test.tsx`, inside `describe("AppDetailsPage", …)`:

```tsx
  it("opens Configuration from its new name and from the old ?tab=editor", async () => {
    renderPage("/app/a1?tab=configuration");
    expect(await screen.findByRole("tab", { name: "Configuration" })).toHaveAttribute("aria-selected", "true");
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/underline-tabs.test.tsx src/hooks/use-tab-param.test.tsx src/pages/app-details.test.tsx`
Expected: FAIL — the wrapper and the hook do not exist; the app page has no `configuration` tab yet (it falls back to Logs).

- [ ] **Step 3: Implement**

Create `web/src/components/underline-tabs.tsx`:

```tsx
import { useEffect, useRef, type ReactNode } from "react";

import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

const triggerClass =
  "-mb-px min-h-10 rounded-none border-b-2 border-transparent px-0.5 pb-2.5 pt-1 text-muted-foreground shadow-none transition-colors duration-150 hover:text-foreground sm:px-1 data-[state=active]:border-foreground data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none";

export type UnderlineTabsProps<T extends string> = {
  value: T;
  onValueChange: (value: T) => void;
  items: { value: T; label: ReactNode }[];
  // children are the panels (TabsContent from components/ui/tabs).
  children: ReactNode;
  // label names the tab list for assistive tech.
  label?: string;
  className?: string;
};

// UnderlineTabs is the app's one tab style: an underline bar that scrolls
// inside itself on a phone (and brings the selected tab into view), over
// the caller's panels. Pair it with useTabParam to keep the tab in ?tab=.
export function UnderlineTabs<T extends string>({
  value,
  onValueChange,
  items,
  children,
  label,
  className,
}: UnderlineTabsProps<T>) {
  const bar = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    bar.current
      ?.querySelector<HTMLElement>('[role="tab"][data-state="active"]')
      ?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
  }, [value]);

  return (
    <Tabs value={value} onValueChange={(next) => onValueChange(next as T)} className={className}>
      <div ref={bar} className="-mx-1 max-w-full overflow-x-auto px-1">
        <TabsList
          aria-label={label}
          className="h-auto w-max min-w-full justify-start gap-6 rounded-none border-b bg-transparent p-0"
        >
          {items.map((item) => (
            <TabsTrigger key={item.value} value={item.value} className={triggerClass}>
              {item.label}
            </TabsTrigger>
          ))}
        </TabsList>
      </div>
      {children}
    </Tabs>
  );
}
```

Create `web/src/hooks/use-tab-param.ts`:

```ts
import { useCallback, useEffect } from "react";
import { useSearchParams } from "react-router-dom";

// useTabParam keeps a page's tab in the URL (?tab=, or opts.param), so a
// link or a reload opens the same tab. A missing or unknown value is the
// fallback tab, which stays out of the URL; an old name listed in
// opts.aliases opens its new tab and is rewritten in place.
export function useTabParam<T extends string>(
  tabs: readonly T[],
  fallback: T,
  opts: { param?: string; aliases?: Readonly<Record<string, T>> } = {},
): [T, (next: T) => void] {
  const param = opts.param ?? "tab";
  const [params, setParams] = useSearchParams();
  const raw = params.get(param);
  const aliased = raw !== null ? opts.aliases?.[raw] : undefined;
  const wanted = aliased ?? raw;
  const tab = wanted !== null && (tabs as readonly string[]).includes(wanted) ? (wanted as T) : fallback;

  useEffect(() => {
    if (aliased === undefined) return;
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set(param, aliased);
        return next;
      },
      { replace: true },
    );
  }, [aliased, param, setParams]);

  const setTab = useCallback(
    (next: T) => {
      setParams(
        (prev) => {
          const out = new URLSearchParams(prev);
          if (next === fallback) out.delete(param);
          else out.set(param, next);
          return out;
        },
        { replace: true },
      );
    },
    [fallback, param, setParams],
  );

  return [tab, setTab];
}
```

In `web/src/pages/app-details.tsx`:

1. Imports: change `import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";` to `import { TabsContent } from "@/components/ui/tabs";` and add

```tsx
import { UnderlineTabs } from "@/components/underline-tabs";
import { useTabParam } from "@/hooks/use-tab-param";
import { LEGACY_APP_TABS } from "@/lib/routes";
```

2. Replace

```tsx
const TABS = ["logs", "metrics", "editor", "backups", "history", "settings"] as const;

const tabTriggerClass =
  "-mb-px rounded-none border-b-2 border-transparent px-0.5 pt-1 sm:px-1 pb-2.5 text-muted-foreground shadow-none hover:text-foreground data-[state=active]:border-foreground data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none";
```

with

```tsx
// History stays a tab until phase 2 turns it into a side sheet.
const TABS = ["logs", "metrics", "configuration", "backups", "history", "settings"] as const;
type Tab = (typeof TABS)[number];
```

3. Replace the tab state block

```tsx
  const tabParam = searchParams.get("tab") ?? "logs";
  const tab = (TABS as readonly string[]).includes(tabParam)
    ? tabParam
    : "logs";
  const setTab = (next: string) => {
    setSearchParams(next === "logs" ? {} : { tab: next }, { replace: true });
  };
  // A tab opened by link may sit past the bar's scrolled edge on a phone.
  const tabBar = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    tabBar.current
      ?.querySelector<HTMLElement>('[role="tab"][data-state="active"]')
      ?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
  }, [tab]);
```

with

```tsx
  const [tab, setTab] = useTabParam<Tab>(TABS, "logs", { aliases: LEGACY_APP_TABS });
```

and remove the now-unused `const [searchParams, setSearchParams] = useSearchParams();` line and the `useSearchParams` import (keep `useRef`/`useEffect` imports only if still used elsewhere in the file — `tsc -b` reports the unused ones).

4. Replace the tab bar — from `<Tabs value={tab} onValueChange={setTab}>` through the closing `</div>` after `</TabsList>` — with:

```tsx
      <UnderlineTabs<Tab>
        value={tab}
        onValueChange={setTab}
        label="App sections"
        items={[
          { value: "logs", label: "Logs" },
          { value: "metrics", label: "Metrics" },
          { value: "configuration", label: "Configuration" },
          {
            value: "backups",
            label: (
              <>
                Backups
                {needsAttention(backupsByApp?.[app.id]) ? (
                  <TriangleAlert
                    role="img"
                    aria-label="A backup is late or failing"
                    className="ml-1.5 inline size-3.5 align-text-bottom text-status-warn"
                  />
                ) : null}
              </>
            ),
          },
          { value: "history", label: "History" },
          { value: "settings", label: "Settings" },
        ]}
      >
```

change `<TabsContent value="editor" className="mt-0">` to `<TabsContent value="configuration" className="mt-0">`, and the closing `</Tabs>` of that block to `</UnderlineTabs>`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/underline-tabs.test.tsx src/hooks/use-tab-param.test.tsx src/pages/app-details.test.tsx`
Expected: PASS (the existing `?tab=editor` tests keep passing through the alias).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/underline-tabs.tsx web/src/components/underline-tabs.test.tsx web/src/hooks/use-tab-param.ts web/src/hooks/use-tab-param.test.tsx web/src/pages/app-details.tsx web/src/pages/app-details.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): one underline tab style synced to ?tab=, app page on it

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 7: The Settings area — layout with sub-menu, server pages, notifications, shared variables

**Files:**
- Create: `web/src/hooks/use-settings-sections.ts`
- Create: `web/src/layouts/settings-layout.tsx`, `web/src/layouts/settings-layout.test.tsx`
- Rename + modify: `web/src/pages/settings.tsx` → `web/src/pages/server-settings.tsx` (four pages instead of the tabbed page)
- Rename + rewrite: `web/src/pages/settings.test.tsx` → `web/src/pages/server-settings.test.tsx`
- Create: `web/src/pages/settings-notifications.tsx`, `web/src/pages/settings-notifications.test.tsx`
- Create: `web/src/pages/settings-shared-variables.tsx`, `web/src/pages/settings-shared-variables.test.tsx`
- Modify: `web/src/pages/org-members.tsx` (`section` prop), `web/src/pages/org-members.test.tsx`
- Modify: `web/src/main.tsx` (keep it compiling: mount the server settings pages; Task 9 replaces the whole table)

**Interfaces:**
- Consumes: `settingsSections`, `SettingsSection` (Task 5), `SETTINGS_PATH`, `serverSettingsPath`, `settingsPath` (Task 5), `PageHeader`, `Section` (Task 3), `useIsMobile` (Task 4), `useServers()` (`activeServer`, `activeServerId`, `servers`, `loading`), `useProfile()` (`profile.email`), `useDocker()` (`hasServer`, …), `DomainsCard`, `StoragesCard`, `VolumesCard`, `VariablesCard`, `OrgVariablesPage`, `RulesTab`, `ChannelsTab`, `createSharedVariablesApi`.
- Produces:
  - `useSettingsSections(): SettingsSection[]` (`@/hooks/use-settings-sections`)
  - `SettingsLayout()` (layout route element: sub-menu left on a desktop; on a phone the list at `/settings`, a "Settings" back link on sub-pages), `SettingsIndex()` (desktop `/settings` → active server's General, else Organization) — `@/layouts/settings-layout`
  - `ServerGeneralPage`, `ServerDomainsPage` (redirects to General without ingress), `ServerStoragesPage`, `ServerDockerPage` — `@/pages/server-settings`
  - default `NotificationsSettingsPage` (`@/pages/settings-notifications`), default `SharedVariablesSettingsPage({ api?: SharedVariablesApi })` (`@/pages/settings-shared-variables`)
  - `OrgMembersPage({ section?: "organization" | "members" })` — no `section` renders both (as before)

- [ ] **Step 1: Write the failing tests**

Create `web/src/layouts/settings-layout.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { SettingsIndex, SettingsLayout } from "@/layouts/settings-layout";
import { stubViewport } from "@/test/media";

type FakeServer = { id: string; name: string; features?: Record<string, boolean> };
let active: FakeServer | null = { id: "s1", name: "Alpha", features: { ingress: true } };
let email = "ann@example.test";

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    activeServer: active,
    activeServerId: active?.id ?? null,
    servers: active ? [active] : [],
    loading: false,
  }),
}));
vi.mock("@/context/use-profile", () => ({
  useProfile: () => ({ profile: { user_id: "u1", name: "Ann", email, must_change_password: false } }),
}));

function Where() {
  return <span data-testid="at">{useLocation().pathname}</span>;
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route index element={<SettingsIndex />} />
          <Route path="organization" element={<p>organization body</p>} />
          <Route path="members" element={<p>members body</p>} />
        </Route>
        <Route path="/server/:serverId/settings" element={<SettingsLayout />}>
          <Route path="general" element={<p>general body</p>} />
        </Route>
      </Routes>
      <Where />
    </MemoryRouter>,
  );
}

describe("SettingsLayout", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    active = { id: "s1", name: "Alpha", features: { ingress: true } };
    email = "ann@example.test";
  });

  it("puts the sub-menu beside the page on a desktop", () => {
    renderAt("/settings/members");
    const menu = screen.getByRole("navigation", { name: "Settings" });
    expect(within(menu).getByText("Server · Alpha")).toBeInTheDocument();
    expect(within(menu).getByText("Organization")).toBeInTheDocument();
    expect(within(menu).getByText("Account")).toBeInTheDocument();
    expect(within(menu).getByRole("link", { name: "Members" })).toHaveAttribute("aria-current", "page");
    expect(within(menu).getByRole("link", { name: "Domains" })).toHaveAttribute("href", "/server/s1/settings/domains");
    expect(screen.getByText("members body")).toBeInTheDocument();
  });

  it("leaves Domains out on a server without ingress and Password out without local credentials", () => {
    active = { id: "s1", name: "Alpha", features: {} };
    email = "";
    renderAt("/settings/members");
    const menu = screen.getByRole("navigation", { name: "Settings" });
    expect(within(menu).queryByRole("link", { name: "Domains" })).not.toBeInTheDocument();
    expect(within(menu).queryByRole("link", { name: "Password" })).not.toBeInTheDocument();
    expect(within(menu).getByRole("link", { name: "API tokens" })).toBeInTheDocument();
  });

  it("opens the active server's General page at /settings on a desktop", async () => {
    renderAt("/settings");
    expect(await screen.findByText("general body")).toBeInTheDocument();
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/settings/general");
  });

  it("opens Organization at /settings when there is no server", async () => {
    active = null;
    renderAt("/settings");
    expect(await screen.findByText("organization body")).toBeInTheDocument();
  });

  it("is a list to drill into at /settings on a phone", () => {
    stubViewport(375);
    renderAt("/settings");
    expect(screen.getByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Members" })).toHaveAttribute("href", "/settings/members");
    expect(screen.getByTestId("at")).toHaveTextContent(/^\/settings$/);
  });

  it("shows a sub-page alone with a way back on a phone", () => {
    stubViewport(375);
    renderAt("/settings/members");
    expect(screen.getByText("members body")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Settings" })).toHaveAttribute("href", "/settings");
    expect(screen.queryByRole("navigation", { name: "Settings" })).not.toBeInTheDocument();
  });
});
```

Rename the old page test and replace its content:

```bash
git mv web/src/pages/settings.test.tsx web/src/pages/server-settings.test.tsx
```

`web/src/pages/server-settings.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ServerDockerPage, ServerDomainsPage, ServerGeneralPage, ServerStoragesPage } from "@/pages/server-settings";

let ingress = true;
let hasServer = true;

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServer: hasServer ? { id: "s1", name: "Alpha", features: { ingress } } : null }),
}));
vi.mock("@/context/use-docker", () => ({
  useDocker: () => ({
    hasServer,
    listRegistries: vi.fn().mockResolvedValue([]),
    listNetworks: vi.fn().mockResolvedValue([]),
    updateAgent: vi.fn(),
  }),
}));
vi.mock("@/pages/settings-domains", () => ({ DomainsCard: () => <p>domains card</p> }));
vi.mock("@/pages/settings-volumes", () => ({ VolumesCard: () => <p>volumes card</p> }));
vi.mock("@/pages/settings-storages", () => ({ StoragesCard: () => <p>storages card</p> }));

function renderPage(page: ReactElement) {
  return render(
    <MemoryRouter initialEntries={["/server/s1/settings/x"]}>
      <Routes>
        <Route path="/server/s1/settings/x" element={page} />
        <Route path="/server/s1/settings/general" element={<p>general page</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("server settings pages", () => {
  afterEach(() => {
    ingress = true;
    hasServer = true;
  });

  it("puts the agent update on General", () => {
    renderPage(<ServerGeneralPage />);
    expect(screen.getByRole("heading", { level: 1, name: "General" })).toBeInTheDocument();
    expect(screen.getByText("Alpha")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Update agent" })).toBeInTheDocument();
  });

  it("shows Domains on a server with ingress", () => {
    renderPage(<ServerDomainsPage />);
    expect(screen.getByRole("heading", { level: 1, name: "Domains" })).toBeInTheDocument();
    expect(screen.getByText("domains card")).toBeInTheDocument();
  });

  it("opens General instead on a server without ingress", async () => {
    ingress = false;
    renderPage(<ServerDomainsPage />);
    expect(await screen.findByText("general page")).toBeInTheDocument();
  });

  it("shows Storages", () => {
    renderPage(<ServerStoragesPage />);
    expect(screen.getByRole("heading", { level: 1, name: "Storages" })).toBeInTheDocument();
    expect(screen.getByText("storages card")).toBeInTheDocument();
  });

  it("stacks registries, networks and volumes on Docker", async () => {
    renderPage(<ServerDockerPage />);
    expect(screen.getByRole("heading", { level: 1, name: "Docker" })).toBeInTheDocument();
    expect(await screen.findByText("No registries.")).toBeInTheDocument();
    expect(await screen.findByText("No networks.")).toBeInTheDocument();
    expect(screen.getByText("volumes card")).toBeInTheDocument();
  });

  it("asks for a server when there is none", () => {
    hasServer = false;
    renderPage(<ServerStoragesPage />);
    expect(screen.getByText("Select a server to manage its settings.")).toBeInTheDocument();
  });
});
```

Create `web/src/pages/settings-notifications.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import NotificationsSettingsPage from "@/pages/settings-notifications";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers: [] }) }));

afterEach(() => vi.unstubAllGlobals());

describe("NotificationsSettingsPage", () => {
  it("puts the channels and the alert rules on one page", async () => {
    fakeAPI({ "/api/v1/alerts/get-rules": [], "/api/v1/alerts/get-channels": [] });
    render(
      <MemoryRouter>
        <NotificationsSettingsPage />
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { level: 1, name: "Notifications" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Channels" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Alert rules" })).toBeInTheDocument();
    expect(await screen.findByText("No channels yet")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /Add rule/ })).toBeInTheDocument();
  });
});
```

Create `web/src/pages/settings-shared-variables.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import SharedVariablesSettingsPage from "@/pages/settings-shared-variables";

let active: { id: string; name: string } | null = { id: "s1", name: "Alpha" };
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServer: active }) }));
vi.mock("@/pages/org-variables", () => ({ default: () => <p>org variables</p> }));
vi.mock("@/pages/settings-variables", () => ({
  VariablesCard: ({ serverId }: { serverId: string }) => <p>server variables {serverId}</p>,
}));
vi.mock("@/lib/shared-variables", () => ({ createSharedVariablesApi: () => ({}) }));

describe("SharedVariablesSettingsPage", () => {
  afterEach(() => {
    active = { id: "s1", name: "Alpha" };
  });

  it("shows the organization's variables, then this server's own", () => {
    render(<SharedVariablesSettingsPage />);
    expect(screen.getByRole("heading", { level: 1, name: "Shared variables" })).toBeInTheDocument();
    expect(screen.getByText("org variables")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Alpha only" })).toHaveTextContent("server variables s1");
  });

  it("shows only the organization's without a server", () => {
    active = null;
    render(<SharedVariablesSettingsPage />);
    expect(screen.getByText("org variables")).toBeInTheDocument();
    expect(screen.queryByText(/server variables/)).not.toBeInTheDocument();
  });
});
```

In `web/src/pages/org-members.test.tsx` change `renderPage` to take the section and add two tests inside `describe("OrgMembersPage", …)`:

```tsx
function renderPage(section?: "organization" | "members") {
  const profile = {
    profile: { user_id: ME.id, name: ME.name, email: ME.email, must_change_password: false },
  } as ProfileContextValue;
  return render(
    <ProfileContext.Provider value={profile}>
      <OrgMembersPage section={section} />
    </ProfileContext.Provider>,
  );
}
```

```tsx
  it("shows only the organization as Settings → Organization", async () => {
    renderPage("organization");
    expect(screen.getByRole("heading", { level: 1, name: "Organization" })).toBeInTheDocument();
    expect(await screen.findByText("Homelab")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Add user/ })).not.toBeInTheDocument();
  });

  it("shows only the members as Settings → Members", async () => {
    renderPage("members");
    expect(screen.getByRole("heading", { level: 1, name: "Members" })).toBeInTheDocument();
    expect(await screen.findByText("Bob")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Edit/ })).not.toBeInTheDocument();
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/layouts/settings-layout.test.tsx src/pages/server-settings.test.tsx src/pages/settings-notifications.test.tsx src/pages/settings-shared-variables.test.tsx src/pages/org-members.test.tsx`
Expected: FAIL — the layout, the pages and `@/pages/server-settings` do not exist; `OrgMembersPage` ignores `section`.

- [ ] **Step 3: Implement**

Create `web/src/hooks/use-settings-sections.ts`:

```ts
import { useMemo } from "react";

import { useProfile } from "@/context/use-profile";
import { useServers } from "@/context/use-servers";
import { settingsSections, type SettingsSection } from "@/lib/settings-nav";

// useSettingsSections is the Settings sub-menu for the active server and
// the signed-in account.
export function useSettingsSections(): SettingsSection[] {
  const { activeServer } = useServers();
  const { profile } = useProfile();
  const id = activeServer?.id ?? null;
  const name = activeServer?.name ?? "";
  const hasIngress = Boolean(activeServer?.features?.ingress);
  const hasPassword = Boolean(profile?.email);
  return useMemo(
    () => settingsSections({ server: id ? { id, name, hasIngress } : null, hasPassword }),
    [id, name, hasIngress, hasPassword],
  );
}
```

Create `web/src/layouts/settings-layout.tsx`:

```tsx
import { Link, Navigate, NavLink, Outlet, useLocation } from "react-router-dom";
import { ChevronLeft, ChevronRight } from "lucide-react";

import { PageHeader } from "@/components/page-header";
import { useServers } from "@/context/use-servers";
import { useIsMobile } from "@/hooks/use-mobile";
import { useSettingsSections } from "@/hooks/use-settings-sections";
import { SETTINGS_PATH, serverSettingsPath, settingsPath } from "@/lib/routes";
import type { SettingsSection } from "@/lib/settings-nav";
import { cn } from "@/lib/utils";

function SettingsMenu({ sections, large = false }: { sections: SettingsSection[]; large?: boolean }) {
  return (
    <nav aria-label="Settings" className="space-y-6">
      {sections.map((section) => (
        <div key={section.title} className="space-y-1">
          <h2 className="truncate px-2 text-xs font-medium text-muted-foreground">{section.title}</h2>
          <ul className="space-y-0.5">
            {section.items.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  className={({ isActive }) =>
                    cn(
                      "flex items-center gap-2 rounded-md px-2 text-sm transition-colors duration-150 hover:bg-accent hover:text-accent-foreground",
                      large ? "min-h-11" : "min-h-8",
                      isActive && "bg-accent font-medium text-accent-foreground",
                    )
                  }
                >
                  <item.icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                  <span className="flex-1 truncate">{item.label}</span>
                  {large ? <ChevronRight className="size-4 text-muted-foreground" aria-hidden /> : null}
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

// SettingsLayout is the one Settings area (spec §6): the sub-menu in a left
// column beside the page on a desktop; on a phone /settings is the menu as
// a list and every sub-page shows alone with a way back to it.
export function SettingsLayout() {
  const sections = useSettingsSections();
  const isMobile = useIsMobile();
  const { pathname } = useLocation();
  const atIndex = pathname.replace(/\/+$/, "") === SETTINGS_PATH;

  if (isMobile) {
    if (atIndex) {
      return (
        <div className="space-y-6">
          <PageHeader title="Settings" />
          <SettingsMenu sections={sections} large />
        </div>
      );
    }
    return (
      <div className="space-y-4">
        <Link
          to={SETTINGS_PATH}
          className="inline-flex min-h-10 items-center gap-1 text-sm text-muted-foreground transition-colors duration-150 hover:text-foreground"
        >
          <ChevronLeft className="size-4" aria-hidden />
          Settings
        </Link>
        <Outlet />
      </div>
    );
  }

  return (
    <div className="flex gap-8">
      <aside className="w-52 shrink-0">
        <SettingsMenu sections={sections} />
      </aside>
      <div className="min-w-0 flex-1">
        <Outlet />
      </div>
    </div>
  );
}

// SettingsIndex is /settings on a desktop: the active server's General
// page, or the organization's when there is no server yet.
export function SettingsIndex() {
  const { activeServerId, servers, loading } = useServers();
  if (loading && servers.length === 0) return null;
  return (
    <Navigate replace to={activeServerId ? serverSettingsPath(activeServerId) : settingsPath("organization")} />
  );
}
```

Rename the page:

```bash
git mv web/src/pages/settings.tsx web/src/pages/server-settings.tsx
```

In `web/src/pages/server-settings.tsx`, replace everything from the first line down to (and including) the end of `export default function SettingsPage() { … }` with:

```tsx
import { useCallback, useEffect, useState } from "react";
import { Navigate } from "react-router-dom";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import {
  useDocker,
  type Docker,
  type Network,
  type Registry,
} from "@/context/use-docker";
import { useServers } from "@/context/use-servers";
import { serverSettingsPath } from "@/lib/routes";
import { DomainsCard } from "@/pages/settings-domains";
import { StoragesCard } from "@/pages/settings-storages";
import { VolumesCard } from "@/pages/settings-volumes";

// Settings → Server: one page per section (the sub-menu is SettingsLayout's).
// Each addresses the server in the URL through useDocker / useServers.

function NoServer() {
  return (
    <p className="text-sm text-muted-foreground">
      Select a server to manage its settings.
    </p>
  );
}

// ServerGeneralPage is the server's General page; for now the agent update
// (phase 5 adds the server's facts).
export function ServerGeneralPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  return (
    <div className="space-y-6">
      <PageHeader title="General" subtitle={activeServer.name} />
      <AgentCard docker={docker} />
    </div>
  );
}

// ServerDomainsPage configures the embedded ingress. A server without it
// (switched to from another server's Domains page, or an old link) opens
// its General page instead.
export function ServerDomainsPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  if (!activeServer.features?.ingress) {
    return <Navigate replace to={serverSettingsPath(activeServer.id)} />;
  }
  return (
    <div className="space-y-6">
      <PageHeader title="Domains" subtitle={activeServer.name} />
      <DomainsCard docker={docker} />
    </div>
  );
}

export function ServerStoragesPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  return (
    <div className="space-y-6">
      <PageHeader title="Storages" subtitle={activeServer.name} />
      <StoragesCard docker={docker} />
    </div>
  );
}

// ServerDockerPage stacks the Docker resources: registries, networks,
// volumes.
export function ServerDockerPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  return (
    <div className="space-y-6">
      <PageHeader title="Docker" subtitle={activeServer.name} />
      <RegistriesCard docker={docker} />
      <NetworksCard docker={docker} />
      <VolumesCard docker={docker} />
    </div>
  );
}
```

Leave `AgentCard`, `RegistriesCard` and `NetworksCard` below it unchanged.

Create `web/src/pages/settings-notifications.tsx`:

```tsx
import { ChannelsTab } from "@/components/alerts/channels-tab";
import { RulesTab } from "@/components/alerts/rules-tab";
import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/section";

// NotificationsSettingsPage is Settings → Notifications: where alerts are
// sent (channels) and when they fire (rules), on one page. Phase 5
// redesigns both; the existing components are mounted as they are.
export default function NotificationsSettingsPage() {
  return (
    <div className="space-y-8">
      <PageHeader title="Notifications" subtitle="Where alerts are sent, and when they fire." />
      <Section title="Channels">
        <ChannelsTab />
      </Section>
      <Section title="Alert rules">
        <RulesTab />
      </Section>
    </div>
  );
}
```

Create `web/src/pages/settings-shared-variables.tsx`:

```tsx
import { useMemo } from "react";

import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/section";
import { useServers } from "@/context/use-servers";
import { createSharedVariablesApi, type SharedVariablesApi } from "@/lib/shared-variables";
import OrgVariablesPage from "@/pages/org-variables";
import { VariablesCard } from "@/pages/settings-variables";

// SharedVariablesSettingsPage is Settings → Shared variables: the
// organization's variables, then the active server's own (formerly the
// server settings' Variables tab). Phase 5 merges them into one list.
export default function SharedVariablesSettingsPage({ api: injected }: { api?: SharedVariablesApi }) {
  const api = useMemo(() => injected ?? createSharedVariablesApi(), [injected]);
  const { activeServer } = useServers();
  return (
    <div className="space-y-8">
      <PageHeader title="Shared variables" subtitle="Values every app can use as ${NAME}." />
      <OrgVariablesPage api={api} />
      {activeServer ? (
        <Section title={`${activeServer.name} only`}>
          <VariablesCard key={activeServer.id} serverId={activeServer.id} api={api} />
        </Section>
      ) : null}
    </div>
  );
}
```

In `web/src/pages/org-members.tsx`:
- change the signature to

```tsx
// section picks one half for Settings → Organization / Members; without it
// the page shows both (the pre-rebuild Organization page).
export default function OrgMembersPage({ section }: { section?: "organization" | "members" }) {
```

- change `<h1 className="text-2xl font-semibold tracking-tight">Organization</h1>` to

```tsx
      <h1 className="text-2xl font-semibold tracking-tight">
        {section === "members" ? "Members" : "Organization"}
      </h1>
```

- wrap the organization `<Card>` (the one with `<CardTitle>Organization</CardTitle>`) in `{section !== "members" ? ( … ) : null}` and the members `<Card>` (with `<CardTitle>Members</CardTitle>`) in `{section !== "organization" ? ( … ) : null}`. The dialogs below stay unconditional.

Keep `web/src/main.tsx` compiling (Task 9 replaces its route table): replace `import SettingsPage from "@/pages/settings"` with

```tsx
import { SettingsLayout } from "@/layouts/settings-layout"
import { ServerDockerPage, ServerDomainsPage, ServerGeneralPage, ServerStoragesPage } from "@/pages/server-settings"
```

add `Navigate` to the `react-router-dom` import, and replace `{ path: "settings", element: <SettingsPage /> },` with

```tsx
              {
                path: "settings",
                element: <SettingsLayout />,
                children: [
                  { index: true, element: <Navigate to="general" replace /> },
                  { path: "general", element: <ServerGeneralPage /> },
                  { path: "domains", element: <ServerDomainsPage /> },
                  { path: "storages", element: <ServerStoragesPage /> },
                  { path: "docker", element: <ServerDockerPage /> },
                ],
              },
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/layouts/settings-layout.test.tsx src/pages/server-settings.test.tsx src/pages/settings-notifications.test.tsx src/pages/settings-shared-variables.test.tsx src/pages/org-members.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/hooks/use-settings-sections.ts web/src/layouts/settings-layout.tsx web/src/layouts/settings-layout.test.tsx web/src/pages/server-settings.tsx web/src/pages/server-settings.test.tsx web/src/pages/settings-notifications.tsx web/src/pages/settings-notifications.test.tsx web/src/pages/settings-shared-variables.tsx web/src/pages/settings-shared-variables.test.tsx web/src/pages/org-members.tsx web/src/pages/org-members.test.tsx web/src/main.tsx
git commit -m "$(cat <<'EOF'
feat(web): one Settings area with a sub-menu and a page per section

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Inbox unread count and the `/inbox` page

**Files:**
- Create: `web/src/lib/inbox.ts`, `web/src/lib/inbox.test.ts`
- Create: `web/src/hooks/use-inbox-unread.ts`, `web/src/hooks/use-inbox-unread.test.tsx`
- Create: `web/src/pages/inbox.tsx`, `web/src/pages/inbox.test.tsx`

**Interfaces:**
- Consumes: `alertsGet` (`@/hooks/use-alerts`), `ALERT_NOTIFICATION`, `AlertEvent` (`@/lib/alerts`), `ProfileContext` (`@/context/profile-context-base`), `NotificationsContext` (`@/context/notifications-context-base`), `HistoryTab` (`@/components/alerts/history-tab`), `PageHeader`, `settingsPath`.
- Produces:
  - `@/lib/inbox`: `INBOX_SEEN_PREFIX = "wf.inbox.seen."`, `inboxSeenKey(userId)`, `readInboxSeen(userId): number | null`, `writeInboxSeen(userId, at: number): void`, `subscribeInboxSeen(onChange): () => void`, `countUnread(events: Pick<AlertEvent, "created_at">[], seenAt: number | null): number`, `unreadBadge(count: number): string` ("99+" above 99)
  - `@/hooks/use-inbox-unread`: `UNREAD_SCAN = 100`, `type InboxUnread = { count: number; seenAt: number | null; isUnread: (createdAt: string) => boolean; markAllRead: () => void }`, `useInboxUnread(): InboxUnread` — reads the newest 100 events on mount, per user and on every `alert` SSE notification (no polling); a first visit seeds last-seen with now; safe outside providers (count 0).
  - default `InboxPage` (`@/pages/inbox`)

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/inbox.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  countUnread,
  inboxSeenKey,
  readInboxSeen,
  subscribeInboxSeen,
  unreadBadge,
  writeInboxSeen,
} from "@/lib/inbox";

describe("inbox last-seen store", () => {
  afterEach(() => localStorage.clear());

  it("keeps one timestamp per user", () => {
    writeInboxSeen("u1", 1000);
    expect(localStorage.getItem(inboxSeenKey("u1"))).toBe("1000");
    expect(readInboxSeen("u1")).toBe(1000);
    expect(readInboxSeen("u2")).toBeNull();
  });

  it("reads garbage as never seen", () => {
    localStorage.setItem(inboxSeenKey("u1"), "yesterday");
    expect(readInboxSeen("u1")).toBeNull();
  });

  it("tells subscribers about a write here and in another tab", () => {
    const seen = vi.fn();
    const unsubscribe = subscribeInboxSeen(seen);
    writeInboxSeen("u1", 5);
    window.dispatchEvent(new StorageEvent("storage", { key: inboxSeenKey("u1") }));
    window.dispatchEvent(new StorageEvent("storage", { key: "wf.theme" }));
    expect(seen).toHaveBeenCalledTimes(2);
    unsubscribe();
    writeInboxSeen("u1", 6);
    expect(seen).toHaveBeenCalledTimes(2);
  });
});

describe("countUnread", () => {
  const events = [{ created_at: "2026-10-13T11:00:00Z" }, { created_at: "2026-10-13T09:00:00Z" }];

  it("counts the events newer than the last visit", () => {
    expect(countUnread(events, Date.parse("2026-10-13T10:00:00Z"))).toBe(1);
  });

  it("counts nothing before the first visit is recorded", () => {
    expect(countUnread(events, null)).toBe(0);
  });
});

describe("unreadBadge", () => {
  it("caps the badge at 99+", () => {
    expect(unreadBadge(3)).toBe("3");
    expect(unreadBadge(99)).toBe("99");
    expect(unreadBadge(100)).toBe("99+");
  });
});
```

Create `web/src/hooks/use-inbox-unread.test.tsx`:

```tsx
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import { ProfileContext, type ProfileContextValue } from "@/context/profile-context-base";
import { useInboxUnread } from "@/hooks/use-inbox-unread";
import { inboxSeenKey, readInboxSeen } from "@/lib/inbox";
import { fakeAPI } from "@/test/fake-api";

function Probe() {
  const { count, isUnread, markAllRead } = useInboxUnread();
  return (
    <div>
      <span data-testid="count">{count}</span>
      <span data-testid="new">{String(isUnread("2026-10-13T11:00:00Z"))}</span>
      <button onClick={markAllRead}>read</button>
    </div>
  );
}

function renderProbe(userId: string | null = "u1") {
  const handlers = new Set<NotificationHandler>();
  const notifications = {
    connected: true,
    subscribe: (h: NotificationHandler) => {
      handlers.add(h);
      return () => handlers.delete(h);
    },
    waitFor: vi.fn(),
  } as unknown as NotificationsContextValue;
  const profile = {
    profile: userId ? { user_id: userId, name: "Ann", email: "", must_change_password: false } : null,
    loading: false,
    refresh: async () => {},
  } as ProfileContextValue;
  render(
    <NotificationsContext.Provider value={notifications}>
      <ProfileContext.Provider value={profile}>
        <Probe />
      </ProfileContext.Provider>
    </NotificationsContext.Provider>,
  );
  return { emit: (n: Notification) => act(() => handlers.forEach((h) => h(n))) };
}

const event = (id: string, at: string) => ({ id, created_at: at, kind: "fired" });
const count = () => screen.getByTestId("count");

describe("useInboxUnread", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => vi.unstubAllGlobals());

  it("counts the events newer than the user's last visit", async () => {
    localStorage.setItem(inboxSeenKey("u1"), String(Date.parse("2026-10-13T10:00:00Z")));
    fakeAPI({
      "/api/v1/alerts/get-events": {
        events: [event("e1", "2026-10-13T11:00:00Z"), event("e2", "2026-10-13T09:00:00Z")],
        next_cursor: "",
      },
    });
    renderProbe();
    await waitFor(() => expect(count()).toHaveTextContent("1"));
    expect(screen.getByTestId("new")).toHaveTextContent("true");
  });

  it("starts from now on a first visit instead of counting old history", async () => {
    fakeAPI({ "/api/v1/alerts/get-events": { events: [event("e1", "2020-01-01T00:00:00Z")], next_cursor: "" } });
    renderProbe();
    await waitFor(() => expect(readInboxSeen("u1")).not.toBeNull());
    expect(count()).toHaveTextContent("0");
  });

  it("marks everything read, even an event stamped ahead of this browser's clock", async () => {
    localStorage.setItem(inboxSeenKey("u1"), "0");
    fakeAPI({ "/api/v1/alerts/get-events": { events: [event("e1", "2999-01-01T00:00:00Z")], next_cursor: "" } });
    renderProbe();
    await waitFor(() => expect(count()).toHaveTextContent("1"));
    await userEvent.setup().click(screen.getByRole("button", { name: "read" }));
    expect(count()).toHaveTextContent("0");
  });

  it("follows a mark made in another tab", async () => {
    localStorage.setItem(inboxSeenKey("u1"), "0");
    fakeAPI({ "/api/v1/alerts/get-events": { events: [event("e1", "2026-10-13T11:00:00Z")], next_cursor: "" } });
    renderProbe();
    await waitFor(() => expect(count()).toHaveTextContent("1"));
    act(() => {
      localStorage.setItem(inboxSeenKey("u1"), String(Date.parse("2027-01-01T00:00:00Z")));
      window.dispatchEvent(new StorageEvent("storage", { key: inboxSeenKey("u1") }));
    });
    expect(count()).toHaveTextContent("0");
  });

  it("re-reads the events when an alert fires", async () => {
    localStorage.setItem(inboxSeenKey("u1"), "0");
    let events: ReturnType<typeof event>[] = [];
    fakeAPI({ "/api/v1/alerts/get-events": () => ({ events, next_cursor: "" }) });
    const { emit } = renderProbe();
    await waitFor(() => expect(count()).toHaveTextContent("0"));
    events = [event("e1", "2026-10-13T11:00:00Z")];
    emit({ type: "alert", ref: "" });
    await waitFor(() => expect(count()).toHaveTextContent("1"));
  });

  it("counts nothing and reads nothing without a signed-in user", () => {
    const api = fakeAPI({ "/api/v1/alerts/get-events": { events: [], next_cursor: "" } });
    renderProbe(null);
    expect(count()).toHaveTextContent("0");
    expect(api.calls).toHaveLength(0);
  });

  it("keeps quiet when the events can't be read", async () => {
    localStorage.setItem(inboxSeenKey("u1"), "0");
    const api = fakeAPI({ "/api/v1/alerts/get-events": new Error("down") });
    renderProbe();
    await waitFor(() => expect(api.calls).toHaveLength(1));
    expect(count()).toHaveTextContent("0");
  });
});
```

Create `web/src/pages/inbox.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import InboxPage from "@/pages/inbox";
import { fakeAPI } from "@/test/fake-api";

const markAllRead = vi.fn();
let unread = 2;
vi.mock("@/hooks/use-inbox-unread", () => ({
  useInboxUnread: () => ({ count: unread, seenAt: 0, isUnread: () => true, markAllRead }),
}));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers: [] }) }));

afterEach(() => {
  vi.unstubAllGlobals();
  unread = 2;
});

function renderPage() {
  fakeAPI({ "/api/v1/alerts/get-events": { events: [], next_cursor: "" } });
  return render(
    <MemoryRouter>
      <InboxPage />
    </MemoryRouter>,
  );
}

describe("InboxPage", () => {
  it("is the org's alert history with a way to the notification settings", async () => {
    renderPage();
    expect(screen.getByRole("heading", { level: 1, name: "Inbox" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Notification settings" })).toHaveAttribute("href", "/settings/notifications");
    expect(await screen.findByText(/No alerts yet/)).toBeInTheDocument();
  });

  it("marks everything read", async () => {
    renderPage();
    await userEvent.setup().click(screen.getByRole("button", { name: "Mark all as read" }));
    expect(markAllRead).toHaveBeenCalled();
  });

  it("offers Mark all as read only when something is unread", () => {
    unread = 0;
    renderPage();
    expect(screen.queryByRole("button", { name: "Mark all as read" })).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/inbox.test.ts src/hooks/use-inbox-unread.test.tsx src/pages/inbox.test.tsx`
Expected: FAIL — modules not found.

- [ ] **Step 3: Implement**

Create `web/src/lib/inbox.ts`:

```ts
import type { AlertEvent } from "@/lib/alerts";

// The Inbox's read state lives in this browser (spec §5: no backend
// change): per user, the time of their last "Mark all as read" (or of
// their first visit). Events newer than it are unread.
export const INBOX_SEEN_PREFIX = "wf.inbox.seen.";

const listeners = new Set<() => void>();

export function inboxSeenKey(userId: string): string {
  return `${INBOX_SEEN_PREFIX}${userId}`;
}

// readInboxSeen is the user's last-seen time (ms), null when never
// recorded or unreadable.
export function readInboxSeen(userId: string): number | null {
  try {
    const raw = window.localStorage.getItem(inboxSeenKey(userId));
    if (raw === null) return null;
    const at = Number(raw);
    return Number.isFinite(at) ? at : null;
  } catch {
    return null;
  }
}

export function writeInboxSeen(userId: string, at: number): void {
  try {
    window.localStorage.setItem(inboxSeenKey(userId), String(at));
  } catch {
    // Storage blocked: nothing counts as unread (readInboxSeen stays null).
  }
  listeners.forEach((l) => l());
}

// subscribeInboxSeen calls onChange after a write here or in another tab.
export function subscribeInboxSeen(onChange: () => void): () => void {
  listeners.add(onChange);
  const onStorage = (event: StorageEvent) => {
    if (event.key?.startsWith(INBOX_SEEN_PREFIX)) onChange();
  };
  window.addEventListener("storage", onStorage);
  return () => {
    listeners.delete(onChange);
    window.removeEventListener("storage", onStorage);
  };
}

// countUnread counts the events created after seenAt (none before the
// first visit is recorded).
export function countUnread(events: Pick<AlertEvent, "created_at">[], seenAt: number | null): number {
  if (seenAt === null) return 0;
  return events.filter((e) => Date.parse(e.created_at) > seenAt).length;
}

// unreadBadge is the sidebar badge text.
export function unreadBadge(count: number): string {
  return count > 99 ? "99+" : String(count);
}
```

Create `web/src/hooks/use-inbox-unread.ts`:

```ts
import { useCallback, useContext, useEffect, useState, useSyncExternalStore } from "react";

import { NotificationsContext } from "@/context/notifications-context-base";
import { ProfileContext } from "@/context/profile-context-base";
import { alertsGet } from "@/hooks/use-alerts";
import { ALERT_NOTIFICATION, type AlertEvent } from "@/lib/alerts";
import { countUnread, readInboxSeen, subscribeInboxSeen, writeInboxSeen } from "@/lib/inbox";

// How many of the newest events the unread count looks at (the badge says
// 99+ above 99 anyway).
export const UNREAD_SCAN = 100;

type EventsPage = { events: AlertEvent[] | null; next_cursor: string };

export type InboxUnread = {
  count: number;
  // seenAt is the user's last-seen time (ms), null before it is recorded.
  seenAt: number | null;
  isUnread: (createdAt: string) => boolean;
  // markAllRead moves last-seen to now, or past the newest event when the
  // server's clock is ahead of this browser's.
  markAllRead: () => void;
};

// useInboxUnread is the signed-in user's unread Inbox count. It reads the
// newest events when mounted and again on every alert notification (no
// polling: idle cost stays zero). A first visit starts counting from now,
// so an install's old history isn't "unread". Outside the providers (or
// signed out) it counts nothing.
export function useInboxUnread(): InboxUnread {
  const userId = useContext(ProfileContext)?.profile?.user_id ?? null;
  const subscribe = useContext(NotificationsContext)?.subscribe;
  const seenAt = useSyncExternalStore(
    subscribeInboxSeen,
    () => (userId ? readInboxSeen(userId) : null),
    () => null,
  );
  const [events, setEvents] = useState<AlertEvent[]>([]);
  const [version, setVersion] = useState(0);

  useEffect(() => {
    if (userId && readInboxSeen(userId) === null) writeInboxSeen(userId, Date.now());
  }, [userId]);

  useEffect(() => {
    if (!userId) return;
    let active = true;
    alertsGet<EventsPage>("get-events", { limit: String(UNREAD_SCAN) })
      .then((page) => {
        if (active) setEvents(page.events ?? []);
      })
      .catch(() => {
        // The badge is best effort: keep the last count.
      });
    return () => {
      active = false;
    };
  }, [userId, version]);

  useEffect(() => {
    if (!subscribe) return;
    return subscribe((n) => {
      if (n.type === ALERT_NOTIFICATION) setVersion((v) => v + 1);
    });
  }, [subscribe]);

  const isUnread = useCallback(
    (createdAt: string) => seenAt !== null && Date.parse(createdAt) > seenAt,
    [seenAt],
  );
  const markAllRead = useCallback(() => {
    if (!userId) return;
    const newest = events.map((e) => Date.parse(e.created_at)).filter(Number.isFinite);
    writeInboxSeen(userId, Math.max(Date.now(), ...newest));
  }, [userId, events]);

  return { count: countUnread(events, seenAt), seenAt, isUnread, markAllRead };
}
```

Create `web/src/pages/inbox.tsx`:

```tsx
import { Link } from "react-router-dom";
import { ArrowRight } from "lucide-react";

import { HistoryTab } from "@/components/alerts/history-tab";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { useInboxUnread } from "@/hooks/use-inbox-unread";
import { settingsPath } from "@/lib/routes";

// InboxPage is /inbox, org-wide: for now the alert history as it was on
// Org → Alerts (phase 4 redesigns it) with Mark all as read, which clears
// the sidebar badge.
export default function InboxPage() {
  const { count, markAllRead } = useInboxUnread();
  return (
    <div className="space-y-6">
      <PageHeader
        title="Inbox"
        subtitle="Alerts from every server."
        actions={
          <>
            {count > 0 ? (
              <Button variant="outline" size="sm" onClick={markAllRead}>
                Mark all as read
              </Button>
            ) : null}
            <Button asChild variant="ghost" size="sm">
              <Link to={settingsPath("notifications")}>
                Notification settings
                <ArrowRight className="size-4" aria-hidden />
              </Link>
            </Button>
          </>
        }
      />
      <HistoryTab />
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/inbox.test.ts src/hooks/use-inbox-unread.test.tsx src/pages/inbox.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/inbox.ts web/src/lib/inbox.test.ts web/src/hooks/use-inbox-unread.ts web/src/hooks/use-inbox-unread.test.tsx web/src/pages/inbox.tsx web/src/pages/inbox.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Inbox page and a per-user unread count kept in the browser

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 9: The route table and redirects from every old URL

**Files:**
- Create: `web/src/components/legacy-redirect.tsx`
- Create: `web/src/pages/new-app.tsx`
- Create: `web/src/routes.tsx`, `web/src/routes.test.tsx`
- Modify: `web/src/main.tsx` (the router uses `appRoutes`)
- Modify: `web/src/layouts/app-layout.tsx` (temporary-password guard → `/settings/password`)
- Delete: `web/src/pages/org-alerts.tsx`, `web/src/pages/org-alerts.test.tsx` (its tabs are now `/inbox` and `/settings/notifications`)

**Interfaces:**
- Consumes: everything in `@/lib/routes` (Task 5), `SettingsLayout`, `SettingsIndex` (Task 7), server settings pages, `NotificationsSettingsPage`, `SharedVariablesSettingsPage` (Task 7), `InboxPage` (Task 8), `DefaultServer`, `ServerScope` (`@/components/server-routes`), existing page components.
- Produces:
  - `LegacyRedirect({ to: RedirectTarget; keepQuery?: boolean /* true */ })`, `type RedirectTarget = string | ((at: { params: Readonly<Params<string>>; search: URLSearchParams }) => string)` (`@/components/legacy-redirect`)
  - default `NewAppPage` (`/server/:id/new`: `?from=template` → the template form, otherwise the compose editor, until phase 3)
  - `appRoutes: RouteObject[]` (`@/routes`) — the children of the signed-in `AppLayout` route:

| Path | Element |
|---|---|
| `/` | `DefaultServer` (→ `/server/:active`) |
| `/server/:serverId` | `HomePage` (Apps) |
| `/server/:serverId/new` | `NewAppPage` |
| `/server/:serverId/app/:appId` | `AppDetailsPage` |
| `/server/:serverId/monitoring` | `MonitoringPage` |
| `/server/:serverId/settings` | `LegacyRedirect` (`?tab=` → section, default General) |
| `/server/:serverId/settings/{general,domains,storages,docker}` | server settings pages in `SettingsLayout` |
| `/inbox` | `InboxPage` |
| `/templates`, `/templates/new`, `/templates/:templateId` | `OrgTemplatesPage`, `TemplatePage` |
| `/settings` | `SettingsIndex` in `SettingsLayout` (phone: the list) |
| `/settings/{organization,members,variables,notifications,password,tokens}` | `OrgMembersPage section=…`, `SharedVariablesSettingsPage`, `NotificationsSettingsPage`, `UserPasswordPage`, `UserTokensPage` |
| old: `/server/:id/create-app`, `/server/:id/create-app/template`, `/org/members`, `/org/variables`, `/org/alerts`, `/org/templates`, `/org/templates/new`, `/org/templates/:id`, `/user/password`, `/user/tokens` | `LegacyRedirect` to the new place |
| `*` | `NotFound` |

- [ ] **Step 1: Write the failing test**

Create `web/src/routes.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { Outlet, RouterProvider, createMemoryRouter, useLocation, useParams } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { appRoutes } from "@/routes";

type FakeServer = { id: string; name: string; features?: Record<string, boolean> };
const ALPHA: FakeServer = { id: "s1", name: "Alpha", features: { ingress: true } };
let servers: FakeServer[] = [ALPHA];

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    servers,
    activeServer: servers[0] ?? null,
    activeServerId: servers[0]?.id ?? null,
    loading: false,
  }),
}));
vi.mock("@/context/use-profile", () => ({
  useProfile: () => ({ profile: { user_id: "u1", name: "Ann", email: "ann@example.test", must_change_password: false } }),
}));

// Every page is a stub naming itself: this test is about which page an
// address opens.
vi.mock("@/pages/home", () => ({ default: () => <p>apps page</p> }));
vi.mock("@/pages/app-details", () => ({ default: () => <p>app page</p> }));
vi.mock("@/pages/monitoring", () => ({ default: () => <p>monitoring page</p> }));
vi.mock("@/pages/create-app", () => ({ default: () => <p>compose page</p> }));
vi.mock("@/pages/create-from-template", () => ({ default: () => <p>from template page</p> }));
vi.mock("@/pages/inbox", () => ({ default: () => <p>inbox page</p> }));
vi.mock("@/pages/org-members", () => ({
  default: ({ section }: { section?: string }) => <p>org page {section}</p>,
}));
vi.mock("@/pages/org-templates", () => ({ default: () => <p>templates page</p> }));
vi.mock("@/pages/template-page", () => ({
  default: function TemplateStub() {
    return <p>template page {useParams().templateId ?? "new"}</p>;
  },
}));
vi.mock("@/pages/server-settings", () => ({
  ServerGeneralPage: () => <p>server general</p>,
  ServerDomainsPage: () => <p>server domains</p>,
  ServerStoragesPage: () => <p>server storages</p>,
  ServerDockerPage: () => <p>server docker</p>,
}));
vi.mock("@/pages/settings-notifications", () => ({ default: () => <p>notifications page</p> }));
vi.mock("@/pages/settings-shared-variables", () => ({ default: () => <p>shared variables page</p> }));
vi.mock("@/pages/user-password", () => ({ default: () => <p>password page</p> }));
vi.mock("@/pages/user-tokens", () => ({ default: () => <p>tokens page</p> }));

function Where() {
  const { pathname, search, hash } = useLocation();
  return <span data-testid="at">{pathname + search + hash}</span>;
}

function renderAt(url: string) {
  const router = createMemoryRouter(
    [
      {
        element: (
          <>
            <Outlet />
            <Where />
          </>
        ),
        children: appRoutes,
      },
    ],
    { initialEntries: [url] },
  );
  return render(<RouterProvider router={router} />);
}

const at = () => screen.getByTestId("at");

describe("route table", () => {
  afterEach(() => {
    servers = [ALPHA];
  });

  it.each([
    ["/server/s1", "apps page"],
    ["/server/s1/new", "compose page"],
    ["/server/s1/new?from=compose", "compose page"],
    ["/server/s1/new?from=template", "from template page"],
    ["/server/s1/app/a1", "app page"],
    ["/server/s1/monitoring", "monitoring page"],
    ["/server/s1/settings/general", "server general"],
    ["/server/s1/settings/domains", "server domains"],
    ["/server/s1/settings/storages", "server storages"],
    ["/server/s1/settings/docker", "server docker"],
    ["/inbox", "inbox page"],
    ["/templates", "templates page"],
    ["/templates/new", "template page new"],
    ["/templates/t1", "template page t1"],
    ["/settings/organization", "org page organization"],
    ["/settings/members", "org page members"],
    ["/settings/variables", "shared variables page"],
    ["/settings/notifications", "notifications page"],
    ["/settings/password", "password page"],
    ["/settings/tokens", "tokens page"],
  ])("opens %s", async (url, page) => {
    renderAt(url);
    expect(await screen.findByText(page)).toBeInTheDocument();
    expect(at()).toHaveTextContent(url);
  });

  it("keeps the alert e-mails' deep links as they are", async () => {
    renderAt("/server/s1/app/a1?tab=metrics");
    expect(await screen.findByText("app page")).toBeInTheDocument();
    expect(at()).toHaveTextContent("/server/s1/app/a1?tab=metrics");
  });

  it.each([
    ["/", "/server/s1", "apps page"],
    ["/settings", "/server/s1/settings/general", "server general"],
    ["/server/s1/settings", "/server/s1/settings/general", "server general"],
    ["/server/s1/settings?tab=agent", "/server/s1/settings/general", "server general"],
    ["/server/s1/settings?tab=registries", "/server/s1/settings/docker", "server docker"],
    ["/server/s1/settings?tab=networks", "/server/s1/settings/docker", "server docker"],
    ["/server/s1/settings?tab=volumes", "/server/s1/settings/docker", "server docker"],
    ["/server/s1/settings?tab=domains", "/server/s1/settings/domains", "server domains"],
    ["/server/s1/settings?tab=storages", "/server/s1/settings/storages", "server storages"],
    ["/server/s1/settings?tab=variables", "/settings/variables", "shared variables page"],
    ["/server/s1/create-app", "/server/s1/new", "compose page"],
    ["/server/s1/create-app/template", "/server/s1/new?from=template", "from template page"],
    ["/org/members", "/settings/members", "org page members"],
    ["/org/variables", "/settings/variables", "shared variables page"],
    ["/org/alerts", "/inbox", "inbox page"],
    ["/org/alerts?tab=history", "/inbox", "inbox page"],
    ["/org/alerts?tab=rules", "/settings/notifications", "notifications page"],
    ["/org/alerts?tab=channels", "/settings/notifications", "notifications page"],
    ["/org/templates", "/templates", "templates page"],
    ["/org/templates/new", "/templates/new", "template page new"],
    ["/org/templates/t1", "/templates/t1", "template page t1"],
    ["/org/templates/t1#versions", "/templates/t1#versions", "template page t1"],
    ["/user/password", "/settings/password", "password page"],
    ["/user/tokens", "/settings/tokens", "tokens page"],
  ])("sends %s to %s", async (from, to, page) => {
    renderAt(from);
    expect(await screen.findByText(page)).toBeInTheDocument();
    expect(at().textContent).toBe(to);
  });

  it("opens the organization's settings at /settings while there is no server", async () => {
    servers = [];
    renderAt("/settings");
    expect(await screen.findByText("org page organization")).toBeInTheDocument();
  });

  it.each(["/nowhere", "/server/s1/settings/nope", "/org/nothing"])("answers %s with Page not found", async (url) => {
    renderAt(url);
    expect(await screen.findByText("Page not found")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm --dir web exec vitest run src/routes.test.tsx`
Expected: FAIL — `@/routes` not found.

- [ ] **Step 3: Implement**

Create `web/src/components/legacy-redirect.tsx`:

```tsx
import { Navigate, useLocation, useParams, type Params } from "react-router-dom";

import { withQuery } from "@/lib/routes";

export type RedirectTarget =
  | string
  | ((at: { params: Readonly<Params<string>>; search: URLSearchParams }) => string);

// LegacyRedirect sends a pre-rebuild URL (a bookmark, an alert e-mail, a
// link in an open tab) to its new place in one hop, replacing the history
// entry. The query and hash carry over, unless keepQuery is false: then
// the old query (a ?tab=) only chose the target.
export function LegacyRedirect({ to, keepQuery = true }: { to: RedirectTarget; keepQuery?: boolean }) {
  const params = useParams();
  const location = useLocation();
  const target = typeof to === "string" ? to : to({ params, search: new URLSearchParams(location.search) });
  return (
    <Navigate replace to={withQuery(target, keepQuery ? location.search : "", location.hash)} state={location.state} />
  );
}
```

Create `web/src/pages/new-app.tsx`:

```tsx
import { useSearchParams } from "react-router-dom";

import CreateAppPage from "@/pages/create-app";
import CreateFromTemplatePage from "@/pages/create-from-template";

// NewAppPage is /server/:id/new: ?from=template opens the template form,
// anything else the compose editor (which also deploys from Git). Phase 3
// adds the chooser and the per-source forms.
export default function NewAppPage() {
  const [params] = useSearchParams();
  return params.get("from") === "template" ? <CreateFromTemplatePage /> : <CreateAppPage />;
}
```

Create `web/src/routes.tsx`:

```tsx
import type { RouteObject } from "react-router-dom";

import { LegacyRedirect } from "@/components/legacy-redirect";
import { NotFound } from "@/components/not-found";
import { DefaultServer, ServerScope } from "@/components/server-routes";
import { SettingsIndex, SettingsLayout } from "@/layouts/settings-layout";
import {
  INBOX_PATH,
  NEW_TEMPLATE_PATH,
  SETTINGS_PATH,
  TEMPLATES_PATH,
  legacyAlertsTarget,
  legacyServerSettingsTarget,
  newAppPath,
  settingsPath,
  templatePath,
} from "@/lib/routes";
import AppDetailsPage from "@/pages/app-details";
import HomePage from "@/pages/home";
import InboxPage from "@/pages/inbox";
import MonitoringPage from "@/pages/monitoring";
import NewAppPage from "@/pages/new-app";
import OrgMembersPage from "@/pages/org-members";
import OrgTemplatesPage from "@/pages/org-templates";
import {
  ServerDockerPage,
  ServerDomainsPage,
  ServerGeneralPage,
  ServerStoragesPage,
} from "@/pages/server-settings";
import NotificationsSettingsPage from "@/pages/settings-notifications";
import SharedVariablesSettingsPage from "@/pages/settings-shared-variables";
import TemplatePage from "@/pages/template-page";
import UserPasswordPage from "@/pages/user-password";
import UserTokensPage from "@/pages/user-tokens";

// appRoutes is everything inside the signed-in shell (AppLayout): the
// route table of the UX rebuild (spec §2), then every pre-rebuild URL sent
// to its new place, so bookmarks, alert e-mails and open tabs keep working.
export const appRoutes: RouteObject[] = [
  { path: "/", element: <DefaultServer fallback={<HomePage />} /> },
  {
    path: "/server/:serverId",
    element: <ServerScope />,
    children: [
      { index: true, element: <HomePage /> },
      { path: "new", element: <NewAppPage /> },
      { path: "app/:appId", element: <AppDetailsPage /> },
      { path: "monitoring", element: <MonitoringPage /> },
      {
        path: "settings",
        element: <SettingsLayout />,
        children: [
          // The pre-rebuild tabbed page: ?tab= picks the section.
          {
            index: true,
            element: (
              <LegacyRedirect
                keepQuery={false}
                to={({ params, search }) => legacyServerSettingsTarget(params.serverId ?? "", search.get("tab"))}
              />
            ),
          },
          { path: "general", element: <ServerGeneralPage /> },
          { path: "domains", element: <ServerDomainsPage /> },
          { path: "storages", element: <ServerStoragesPage /> },
          { path: "docker", element: <ServerDockerPage /> },
        ],
      },
      // Before the rebuild: Add app and From template.
      { path: "create-app", element: <LegacyRedirect to={({ params }) => newAppPath(params.serverId ?? "")} /> },
      {
        path: "create-app/template",
        element: <LegacyRedirect to={({ params }) => newAppPath(params.serverId ?? "", "template")} />,
      },
    ],
  },
  { path: INBOX_PATH, element: <InboxPage /> },
  { path: TEMPLATES_PATH, element: <OrgTemplatesPage /> },
  { path: NEW_TEMPLATE_PATH, element: <TemplatePage /> },
  { path: `${TEMPLATES_PATH}/:templateId`, element: <TemplatePage /> },
  {
    path: SETTINGS_PATH,
    element: <SettingsLayout />,
    children: [
      { index: true, element: <SettingsIndex /> },
      { path: "organization", element: <OrgMembersPage section="organization" /> },
      { path: "members", element: <OrgMembersPage section="members" /> },
      { path: "variables", element: <SharedVariablesSettingsPage /> },
      { path: "notifications", element: <NotificationsSettingsPage /> },
      { path: "password", element: <UserPasswordPage /> },
      { path: "tokens", element: <UserTokensPage /> },
    ],
  },
  // Before the rebuild: the organization's and the user's pages.
  { path: "/org/members", element: <LegacyRedirect to={settingsPath("members")} /> },
  { path: "/org/variables", element: <LegacyRedirect to={settingsPath("variables")} /> },
  {
    path: "/org/alerts",
    element: <LegacyRedirect keepQuery={false} to={({ search }) => legacyAlertsTarget(search.get("tab"))} />,
  },
  { path: "/org/templates", element: <LegacyRedirect to={TEMPLATES_PATH} /> },
  { path: "/org/templates/new", element: <LegacyRedirect to={NEW_TEMPLATE_PATH} /> },
  {
    path: "/org/templates/:templateId",
    element: <LegacyRedirect to={({ params }) => templatePath(params.templateId ?? "")} />,
  },
  { path: "/user/password", element: <LegacyRedirect to={settingsPath("password")} /> },
  { path: "/user/tokens", element: <LegacyRedirect to={settingsPath("tokens")} /> },
  { path: "*", element: <NotFound /> },
];
```

Replace the whole of `web/src/main.tsx` with:

```tsx
import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { createBrowserRouter, Outlet, RouterProvider } from "react-router-dom"

import "@/styles/inter.css"
import "@/lib/i18n"

import { RequireAuth } from "@/components/require-auth"
import { LogoSprite } from "@/components/app-logo"
import { ThemedToaster } from "@/components/themed-toaster"
import { AuthProvider } from "@/context/auth-context"
import { UserProvider } from "@/context/user-context"
import { ProfileProvider } from "@/context/profile-context"
import { ServersProvider } from "@/context/servers-context"
import { NotificationsProvider } from "@/context/notifications-context"
import { AppsProvider } from "@/context/apps-context"
import { AppLayout } from "@/layouts/app-layout"
import LoginPage from "@/pages/login"
import RegisterPage from "@/pages/register"
import { startTheme } from "@/lib/theme"
import { appRoutes } from "@/routes"
import "./index.css"

// Light, dark or the system's (the user menu and ⌘K switch it); applied
// before the first render so nothing flashes.
startTheme()

// The servers and apps providers sit inside the router: the active server is
// read from the URL. The signed-in pages are the route table in routes.tsx.
const router = createBrowserRouter([
  {
    element: (
      <ServersProvider>
        <AppsProvider>
          <Outlet />
        </AppsProvider>
      </ServersProvider>
    ),
    children: [
      {
        element: (
          <RequireAuth>
            <AppLayout />
          </RequireAuth>
        ),
        children: appRoutes,
      },
      {
        path: "/login",
        element: <LoginPage />,
      },
      {
        path: "/register",
        element: <RegisterPage />,
      },
    ],
  },
])

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AuthProvider>
      <UserProvider>
        <ProfileProvider>
        <NotificationsProvider>
          <LogoSprite />
          <RouterProvider router={router} />
          <ThemedToaster />
        </NotificationsProvider>
        </ProfileProvider>
      </UserProvider>
    </AuthProvider>
  </StrictMode>
)
```

In `web/src/layouts/app-layout.tsx` add `import { settingsPath } from "@/lib/routes"` and replace the guard:

```tsx
  if (
    profile?.must_change_password &&
    location.pathname !== settingsPath("password")
  ) {
    return <Navigate to={settingsPath("password")} replace />
  }
```

Delete the old alerts page:

```bash
git rm web/src/pages/org-alerts.tsx web/src/pages/org-alerts.test.tsx
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/routes.test.tsx src/context/servers-context.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, build once, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b && pnpm --dir web run build && git checkout -- web/dist/.gitkeep
git add web/src/components/legacy-redirect.tsx web/src/pages/new-app.tsx web/src/routes.tsx web/src/routes.test.tsx web/src/main.tsx web/src/layouts/app-layout.tsx
git commit -m "$(cat <<'EOF'
feat(web): the rebuild's route table, with redirects from every old URL

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: Point every internal link at the new routes

**Files:**
- Modify: `web/src/pages/home.tsx`, `web/src/pages/home.test.tsx`
- Modify: `web/src/components/server-links.test.tsx`
- Modify: `web/src/components/instance-panel.tsx`
- Modify: `web/src/pages/app-details.tsx`, `web/src/pages/app-details.test.tsx`
- Modify: `web/src/pages/create-from-template.tsx`, `web/src/pages/create-from-template.test.tsx`
- Modify: `web/src/pages/org-templates.tsx`
- Modify: `web/src/pages/template-page.tsx`, `web/src/pages/template-page.test.tsx`
- Modify: `web/src/components/backup-storage-dialog.tsx`, `web/src/components/backup-storage-dialog.test.tsx`
- Modify: `web/src/components/add-backup-dialog.tsx`, `web/src/components/add-backup-dialog.test.tsx`
- Modify: `web/src/components/host-hints.tsx`, `web/src/components/ingress-editor.test.tsx`
- Modify: `web/src/components/variables-table.tsx`, `web/src/components/variables-table.test.tsx`
- Modify: `web/src/components/alerts/active-alerts.tsx`, `web/src/components/alerts/active-alerts.test.tsx`
- Modify: `web/src/components/alerts/target-alerts-card.tsx`, `web/src/components/alerts/target-alerts-card.test.tsx`

**Interfaces:**
- Consumes: `newAppSub`, `serverSettingsSub`, `settingsPath`, `templatePath`, `TEMPLATES_PATH`, `NEW_TEMPLATE_PATH`, `INBOX_PATH` (Task 5).
- Produces: no new names; after this task only the legacy route table (`routes.tsx` and its tests) still spells a pre-rebuild address.

- [ ] **Step 1: Update the tests to the new addresses (they fail until the code follows)**

- `web/src/pages/home.test.tsx`: the route `<Route path="/create-app/template" …>` becomes `<Route path="/new" element={<p>from template</p>} />` (the page navigates to `/new?from=template` without an active server).
- `web/src/components/server-links.test.tsx`: `<Route path="/server/s1/create-app" element={<p>create page</p>} />` becomes `<Route path="/server/s1/new" element={<p>create page</p>} />`.
- `web/src/pages/app-details.test.tsx`: `<Route path="/org/templates/new" element={<NewTemplate />} />` becomes `<Route path="/templates/new" element={<NewTemplate />} />`; the expectation `toHaveAttribute("href", "/org/templates/t1")` becomes `"/templates/t1"`.
- `web/src/pages/create-from-template.test.tsx`: `initialEntries={["/server/s1/create-app/template"]}` and its `<Route path="/server/s1/create-app/template" …>` become `/server/s1/new` (keep the query out: the page itself doesn't read it); `<Route path="/org/templates" …>` becomes `<Route path="/templates" …>`; the expectation `"/org/templates"` (link "Add one") becomes `"/templates"`.
- `web/src/pages/template-page.test.tsx`: replace every `/org/templates` with `/templates` (routes, `renderPage(...)` URLs, `initialEntries`, and the `href` in the breadcrumb expectation on line 125, which becomes `href: "/templates"`; Task 12 replaces that expectation).
- `web/src/components/backup-storage-dialog.test.tsx:63` and `web/src/components/add-backup-dialog.test.tsx:98`: expect `"/server/srv-1/settings/storages"`.
- `web/src/components/ingress-editor.test.tsx:148`: expect `"/server/srv-1/settings/domains"`.
- `web/src/components/variables-table.test.tsx:77`: the link is now named "Manage shared variables" and points at `/settings/variables`:

```tsx
    expect(within(org).getByRole("link", { name: "Manage shared variables" })).toHaveAttribute("href", "/settings/variables");
```

- `web/src/components/alerts/active-alerts.test.tsx`: in the first `ActiveAlertsStrip` test ("lists firing alerts with links, …"), right after `const strip = await screen.findByRole("region", { name: "Active alerts" });`, add:

```tsx
    expect(within(strip).getByRole("link", { name: "View history" })).toHaveAttribute("href", "/inbox");
```

- `web/src/components/alerts/target-alerts-card.test.tsx`: in the first test ("shows what fires here and every server rule …"), after `const firing = await screen.findByRole("list", { name: "Firing" });`, add:

```tsx
    expect(screen.getByRole("link", { name: "Manage rules" })).toHaveAttribute("href", "/settings/notifications");
```

Run: `pnpm --dir web exec vitest run src/pages/home.test.tsx src/components/server-links.test.tsx src/pages/app-details.test.tsx src/pages/create-from-template.test.tsx src/pages/template-page.test.tsx src/components/backup-storage-dialog.test.tsx src/components/add-backup-dialog.test.tsx src/components/ingress-editor.test.tsx src/components/variables-table.test.tsx src/components/alerts/active-alerts.test.tsx src/components/alerts/target-alerts-card.test.tsx`
Expected: FAIL — the code still links to the old addresses.

- [ ] **Step 2: Change the links**

Each change imports what it uses from `@/lib/routes`.

- `web/src/pages/home.tsx`: `navigate(toServer("/create-app/template"))` → `navigate(toServer(newAppSub("template")))`; `navigate(toServer("/create-app"))` → `navigate(toServer(newAppSub()))`.
- `web/src/components/instance-panel.tsx`: ``to={`/org/templates/${instance.template_id}`}`` → `to={templatePath(instance.template_id)}`.
- `web/src/pages/app-details.tsx`: ``to={`/org/templates/${inst.template_id}`}`` → `to={templatePath(inst.template_id)}`; `navigate("/org/templates/new", { state })` → `navigate(NEW_TEMPLATE_PATH, { state })`.
- `web/src/pages/create-from-template.tsx`: `<Link to="/org/templates" …>` → `<Link to={TEMPLATES_PATH} …>`; `toServer("/settings?tab=storages")` → `toServer(serverSettingsSub("storages"))`.
- `web/src/pages/org-templates.tsx`: `navigate("/org/templates/new")` → `navigate(NEW_TEMPLATE_PATH)`; ``to={`/org/templates/${t.id}`}`` → `to={templatePath(t.id)}`.
- `web/src/pages/template-page.tsx`: ``navigate(`/org/templates/${saved.id}`, { replace: true })`` → `navigate(templatePath(saved.id), { replace: true })`; `navigate("/org/templates")` → `navigate(TEMPLATES_PATH)`; in the breadcrumbs `href: "/org/templates"` → `href: TEMPLATES_PATH` (removed in Task 12); the comment `(/org/templates/:templateId)` → `(/templates/:templateId)`.
- `web/src/components/backup-storage-dialog.tsx` and `web/src/components/add-backup-dialog.tsx`: `toServer("/settings?tab=storages")` → `toServer(serverSettingsSub("storages"))`.
- `web/src/components/host-hints.tsx`: `toServer("/settings?tab=domains")` → `toServer(serverSettingsSub("domains"))`.
- `web/src/components/variables-table.tsx`:

```tsx
            <Link to={settingsPath("variables")} className="text-sm underline-offset-4 hover:underline">
              Manage shared variables
            </Link>
```

- `web/src/components/alerts/active-alerts.tsx`: `<Link to="/org/alerts" …>` → `<Link to={INBOX_PATH} …>`.
- `web/src/components/alerts/target-alerts-card.tsx`: `<Link to="/org/alerts?tab=rules" …>` → `<Link to={settingsPath("notifications")} …>`.

- [ ] **Step 3: Run the tests to verify they pass, and that no old address is left**

Run: `pnpm --dir web exec vitest run src/pages/home.test.tsx src/components/server-links.test.tsx src/pages/app-details.test.tsx src/pages/create-from-template.test.tsx src/pages/template-page.test.tsx src/components/backup-storage-dialog.test.tsx src/components/add-backup-dialog.test.tsx src/components/ingress-editor.test.tsx src/components/variables-table.test.tsx src/components/alerts/active-alerts.test.tsx src/components/alerts/target-alerts-card.test.tsx`
Expected: PASS

Run:

```bash
grep -rnE '/org/(members|alerts|variables|templates)|/user/(tokens|password)|"/create-app|settings\?tab=' web/src --include=*.tsx --include=*.ts \
  | grep -v -e 'src/routes.tsx' -e 'src/routes.test.tsx' -e 'lib/routes.test.ts' -e 'components/nav-user' -e '^[^:]*:[0-9]*: *//'
```

Expected: no output. (`/api/v1/org/...` API paths don't match; `nav-user.tsx` and its test still carry the org items until Task 15 rebuilds the user menu.)

- [ ] **Step 4: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add -u web/src
git commit -m "$(cat <<'EOF'
refactor(web): link to the rebuild's routes instead of the old ones

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 11: Keyboard shortcuts, the palette's model and its action registry

**Files:**
- Create: `web/src/lib/keyboard.ts`, `web/src/lib/keyboard.test.ts`
- Create: `web/src/lib/palette.ts`, `web/src/lib/palette.test.ts`
- Create: `web/src/context/palette-context-base.ts`, `web/src/context/palette-context.tsx`, `web/src/context/use-palette.ts`, `web/src/context/palette-context.test.tsx`

**Interfaces:**
- Consumes: `appLabel` (`@/lib/kpi`), route builders (Task 5), `SettingsSection` (Task 5), `serverPath`.
- Produces:
  - `@/lib/keyboard`: `type KeyLike = Pick<KeyboardEvent, "key" | "metaKey" | "ctrlKey" | "altKey" | "shiftKey">`, `isMacPlatform(): boolean`, `modKeyLabel(): string` ("⌘" or "Ctrl+"), `shortcutLabel(key: string | number): string`, `isPaletteShortcut(e: KeyLike): boolean` (⌘K / Ctrl+K), `serverShortcutIndex(e: KeyLike): number | null` (⌘1…⌘9 → 0…8)
  - `@/lib/palette`: `type PaletteGroup = "Apps" | "Pages" | "Settings" | "Servers" | "Actions"`, `type PaletteNavEntry = { id: string; group: PaletteGroup; label: string; to: string; hint?: string; keywords?: string[] }`, `type RecentEntry = { id: string; label: string; to: string }`, `RECENT_KEY = "wf.palette.recent"`, `RECENT_MAX = 5`, `paletteNavEntries(input: { serverId: string | null; apps: { id: string; name: string }[]; servers: { id: string; name: string }[]; settings: SettingsSection[]; standalone: boolean }): PaletteNavEntry[]`, `readRecent(): RecentEntry[]`, `pushRecent(entry: RecentEntry): void`, `visibleRecent(recent: RecentEntry[], entries: PaletteNavEntry[]): PaletteNavEntry[]`
  - `@/context/palette-context-base`: `type PaletteAction = { id: string; label: string; icon?: LucideIcon; keywords?: string[]; disabled?: boolean; run: () => void }`, `type PaletteActionGroup = { heading: string; actions: PaletteAction[] }`, `type CommandPaletteContextValue = { open: boolean; setOpen: (open: boolean) => void; openPalette: () => void; groups: PaletteActionGroup[]; register: (key: string, group: PaletteActionGroup) => () => void }`, `CommandPaletteContext`
  - `@/context/palette-context`: `CommandPaletteProvider({ children })` — owns the open state and the registry, toggles on ⌘K / Ctrl+K anywhere (also from inputs)
  - `@/context/use-palette`: `useCommandPalette(): CommandPaletteContextValue` (an inert value outside the provider, so components and page tests need no provider), `usePaletteActions(heading: string, actions: PaletteAction[]): void` — registers while mounted; re-registers only when the heading or an action's id/label/disabled changes; `run` always calls the latest handler, so callers may pass a fresh array every render.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/keyboard.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";

import { isMacPlatform, isPaletteShortcut, modKeyLabel, serverShortcutIndex, shortcutLabel } from "@/lib/keyboard";

const key = (k: string, mods: Partial<Record<"metaKey" | "ctrlKey" | "altKey" | "shiftKey", boolean>> = {}) => ({
  key: k,
  metaKey: false,
  ctrlKey: false,
  altKey: false,
  shiftKey: false,
  ...mods,
});

describe("isPaletteShortcut", () => {
  it("is ⌘K or Ctrl+K", () => {
    expect(isPaletteShortcut(key("k", { metaKey: true }))).toBe(true);
    expect(isPaletteShortcut(key("K", { ctrlKey: true }))).toBe(true);
  });

  it("is not a plain K or one with Alt or Shift", () => {
    expect(isPaletteShortcut(key("k"))).toBe(false);
    expect(isPaletteShortcut(key("k", { metaKey: true, shiftKey: true }))).toBe(false);
    expect(isPaletteShortcut(key("k", { ctrlKey: true, altKey: true }))).toBe(false);
  });
});

describe("serverShortcutIndex", () => {
  it("maps ⌘1…⌘9 to the first nine servers", () => {
    expect(serverShortcutIndex(key("1", { metaKey: true }))).toBe(0);
    expect(serverShortcutIndex(key("9", { ctrlKey: true }))).toBe(8);
  });

  it("ignores 0, letters, bare digits and extra modifiers", () => {
    expect(serverShortcutIndex(key("0", { metaKey: true }))).toBeNull();
    expect(serverShortcutIndex(key("a", { metaKey: true }))).toBeNull();
    expect(serverShortcutIndex(key("1"))).toBeNull();
    expect(serverShortcutIndex(key("1", { metaKey: true, altKey: true }))).toBeNull();
    expect(serverShortcutIndex(key("1", { ctrlKey: true, shiftKey: true }))).toBeNull();
  });
});

describe("labels", () => {
  afterEach(() => vi.restoreAllMocks());

  it("says ⌘ on a Mac", () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    expect(isMacPlatform()).toBe(true);
    expect(modKeyLabel()).toBe("⌘");
    expect(shortcutLabel(2)).toBe("⌘2");
  });

  it("says Ctrl+ elsewhere", () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux x86_64");
    vi.spyOn(navigator, "userAgent", "get").mockReturnValue("Mozilla/5.0 (X11; Linux x86_64)");
    expect(isMacPlatform()).toBe(false);
    expect(shortcutLabel("K")).toBe("Ctrl+K");
  });
});
```

Create `web/src/lib/palette.test.ts`:

```ts
import { afterEach, describe, expect, it } from "vitest";

import { RECENT_KEY, paletteNavEntries, pushRecent, readRecent, visibleRecent } from "@/lib/palette";
import { settingsSections } from "@/lib/settings-nav";

const settings = settingsSections({ server: { id: "s1", name: "Alpha", hasIngress: false }, hasPassword: true });
const servers = [
  { id: "s1", name: "Alpha" },
  { id: "s2", name: "Beta" },
];

describe("paletteNavEntries", () => {
  it("lists this server's apps, every page, every Settings section, the other servers and New app", () => {
    const entries = paletteNavEntries({
      serverId: "s1",
      apps: [
        { id: "a1", name: "Shop" },
        { id: "0123456789", name: "" },
      ],
      servers,
      settings,
      standalone: false,
    });
    expect(entries.filter((e) => e.group === "Apps").map((e) => [e.label, e.to])).toEqual([
      ["Shop", "/server/s1/app/a1"],
      ["01234567", "/server/s1/app/0123456789"],
    ]);
    expect(entries.filter((e) => e.group === "Pages").map((e) => [e.label, e.to])).toEqual([
      ["Apps", "/server/s1"],
      ["Monitoring", "/server/s1/monitoring"],
      ["Inbox", "/inbox"],
      ["Templates", "/templates"],
    ]);
    const general = entries.filter((e) => e.group === "Settings" && e.label === "General");
    expect(general.map((e) => [e.hint, e.to])).toEqual([
      ["Server · Alpha", "/server/s1/settings/general"],
      ["Organization", "/settings/organization"],
    ]);
    expect(entries.filter((e) => e.group === "Servers").map((e) => [e.label, e.to])).toEqual([["Beta", "/server/s2"]]);
    expect(entries.filter((e) => e.group === "Actions").map((e) => [e.label, e.to])).toEqual([["New app", "/server/s1/new"]]);
  });

  it("has no server pages without a server, and no servers on a standalone install", () => {
    const none = paletteNavEntries({ serverId: null, apps: [], servers: [], settings, standalone: false });
    expect(none.map((e) => e.label)).not.toContain("Monitoring");
    expect(none.map((e) => e.label)).not.toContain("New app");
    const standalone = paletteNavEntries({ serverId: "s1", apps: [], servers, settings, standalone: true });
    expect(standalone.some((e) => e.group === "Servers")).toBe(false);
  });
});

describe("recent items", () => {
  afterEach(() => localStorage.clear());

  it("keeps the last five, newest first, without repeats", () => {
    for (const n of [1, 2, 3, 4, 5, 6]) pushRecent({ id: `app:${n}`, label: `App ${n}`, to: `/x/${n}` });
    pushRecent({ id: "app:4", label: "App 4", to: "/x/4" });
    expect(readRecent().map((r) => r.id)).toEqual(["app:4", "app:6", "app:5", "app:3", "app:2"]);
  });

  it("reads garbage as no recent items", () => {
    localStorage.setItem(RECENT_KEY, "{not json");
    expect(readRecent()).toEqual([]);
    localStorage.setItem(RECENT_KEY, JSON.stringify({ id: "x" }));
    expect(readRecent()).toEqual([]);
    localStorage.setItem(RECENT_KEY, JSON.stringify([{ id: 1 }, { id: "page:inbox", label: "Inbox", to: "/inbox" }]));
    expect(readRecent()).toEqual([{ id: "page:inbox", label: "Inbox", to: "/inbox" }]);
  });

  it("shows only recent items that still exist, as they are now", () => {
    const entries = paletteNavEntries({ serverId: "s1", apps: [{ id: "a1", name: "Shop v2" }], servers, settings, standalone: false });
    const shown = visibleRecent(
      [
        { id: "app:gone", label: "Gone", to: "/server/s1/app/gone" },
        { id: "app:a1", label: "Shop", to: "/server/s1/app/a1" },
        { id: "page:inbox", label: "Inbox", to: "/inbox" },
      ],
      entries,
    );
    expect(shown.map((e) => e.label)).toEqual(["Shop v2", "Inbox"]);
  });
});
```

Create `web/src/context/palette-context.test.tsx`:

```tsx
import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { CommandPaletteProvider } from "@/context/palette-context";
import { useCommandPalette, usePaletteActions } from "@/context/use-palette";

function State() {
  const { open, openPalette, setOpen } = useCommandPalette();
  return (
    <div>
      <span data-testid="open">{open ? "open" : "closed"}</span>
      <button onClick={openPalette}>open it</button>
      <button onClick={() => setOpen(false)}>close it</button>
    </div>
  );
}

function Page({ label, onRun }: { label: string; onRun: () => void }) {
  usePaletteActions("Shop", [{ id: "restart", label, run: onRun }]);
  return null;
}

function Groups() {
  const { groups } = useCommandPalette();
  return (
    <ul>
      {groups.flatMap((g) =>
        g.actions.map((a) => (
          <li key={`${g.heading}-${a.id}`}>
            <button onClick={a.run}>{`${g.heading}: ${a.label}`}</button>
          </li>
        )),
      )}
    </ul>
  );
}

describe("CommandPaletteProvider", () => {
  it("toggles on ⌘K and Ctrl+K, and opens on request", async () => {
    render(
      <CommandPaletteProvider>
        <State />
      </CommandPaletteProvider>,
    );
    const open = () => screen.getByTestId("open");
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(open()).toHaveTextContent("open");
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    expect(open()).toHaveTextContent("closed");
    fireEvent.keyDown(window, { key: "k", ctrlKey: true, shiftKey: true });
    expect(open()).toHaveTextContent("closed");
    await userEvent.setup().click(screen.getByRole("button", { name: "open it" }));
    expect(open()).toHaveTextContent("open");
  });

  it("keeps ⌘K from reaching the browser", () => {
    render(
      <CommandPaletteProvider>
        <State />
      </CommandPaletteProvider>,
    );
    const event = new KeyboardEvent("keydown", { key: "k", metaKey: true, cancelable: true });
    act(() => {
      window.dispatchEvent(event);
    });
    expect(event.defaultPrevented).toBe(true);
  });

  it("collects the actions a page registers, with the latest handler, until it unmounts", async () => {
    const user = userEvent.setup();
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = render(
      <CommandPaletteProvider>
        <Page label="Restart" onRun={first} />
        <Groups />
      </CommandPaletteProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Shop: Restart" }));
    expect(first).toHaveBeenCalledTimes(1);

    rerender(
      <CommandPaletteProvider>
        <Page label="Restart" onRun={second} />
        <Groups />
      </CommandPaletteProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Shop: Restart" }));
    expect(second).toHaveBeenCalledTimes(1);
    expect(first).toHaveBeenCalledTimes(1);

    rerender(
      <CommandPaletteProvider>
        <Page label="Restart now" onRun={second} />
        <Groups />
      </CommandPaletteProvider>,
    );
    expect(screen.getByRole("button", { name: "Shop: Restart now" })).toBeInTheDocument();

    rerender(
      <CommandPaletteProvider>
        <Groups />
      </CommandPaletteProvider>,
    );
    expect(screen.queryByRole("button", { name: /Shop/ })).not.toBeInTheDocument();
  });

  it("is inert outside the provider", () => {
    render(
      <>
        <Page label="Restart" onRun={vi.fn()} />
        <State />
      </>,
    );
    expect(screen.getByTestId("open")).toHaveTextContent("closed");
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/keyboard.test.ts src/lib/palette.test.ts src/context/palette-context.test.tsx`
Expected: FAIL — modules not found.

- [ ] **Step 3: Implement**

Create `web/src/lib/keyboard.ts`:

```ts
export type KeyLike = Pick<KeyboardEvent, "key" | "metaKey" | "ctrlKey" | "altKey" | "shiftKey">;

// isMacPlatform decides whether shortcuts read ⌘ or Ctrl.
export function isMacPlatform(): boolean {
  if (typeof navigator === "undefined") return false;
  return /Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent);
}

export function modKeyLabel(): string {
  return isMacPlatform() ? "⌘" : "Ctrl+";
}

// shortcutLabel spells ⌘/Ctrl plus a key ("⌘K", "Ctrl+1").
export function shortcutLabel(key: string | number): string {
  return `${modKeyLabel()}${key}`;
}

function commandOnly(e: KeyLike): boolean {
  return (e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey;
}

// isPaletteShortcut is ⌘K / Ctrl+K: it opens the palette from anywhere,
// text fields included.
export function isPaletteShortcut(e: KeyLike): boolean {
  return commandOnly(e) && e.key.toLowerCase() === "k";
}

// serverShortcutIndex maps ⌘1…⌘9 (Ctrl elsewhere) to the index of the
// server to switch to, null for any other key.
export function serverShortcutIndex(e: KeyLike): number | null {
  if (!commandOnly(e) || !/^[1-9]$/.test(e.key)) return null;
  return Number(e.key) - 1;
}
```

Create `web/src/lib/palette.ts`:

```ts
import { appLabel } from "@/lib/kpi";
import {
  INBOX_PATH,
  TEMPLATES_PATH,
  appPath,
  appsPath,
  monitoringPath,
  newAppPath,
} from "@/lib/routes";
import { serverPath } from "@/lib/server-paths";
import type { SettingsSection } from "@/lib/settings-nav";

// The ⌘K palette's destinations (spec §2): the apps on this server, every
// page, every Settings section, the other servers, and New app. Contextual
// actions (an app page's Restart, …) come from the palette context, theme
// and Add server from the palette itself.

export type PaletteGroup = "Apps" | "Pages" | "Settings" | "Servers" | "Actions";

export type PaletteNavEntry = {
  // id is stable across sessions (recent items are stored by it).
  id: string;
  group: PaletteGroup;
  label: string;
  to: string;
  // hint tells same-named entries apart (the Settings section's title).
  hint?: string;
  keywords?: string[];
};

export type RecentEntry = { id: string; label: string; to: string };

export const RECENT_KEY = "wf.palette.recent";
export const RECENT_MAX = 5;

export function paletteNavEntries({
  serverId,
  apps,
  servers,
  settings,
  standalone,
}: {
  serverId: string | null;
  apps: { id: string; name: string }[];
  servers: { id: string; name: string }[];
  settings: SettingsSection[];
  standalone: boolean;
}): PaletteNavEntry[] {
  const entries: PaletteNavEntry[] = [];
  if (serverId) {
    for (const app of apps) {
      entries.push({ id: `app:${app.id}`, group: "Apps", label: appLabel(app.name, app.id), to: appPath(serverId, app.id), keywords: ["app"] });
    }
    entries.push(
      { id: "page:apps", group: "Pages", label: "Apps", to: appsPath(serverId), keywords: ["home", "dashboard"] },
      { id: "page:monitoring", group: "Pages", label: "Monitoring", to: monitoringPath(serverId), keywords: ["metrics", "cpu", "memory", "disk"] },
    );
  }
  entries.push(
    { id: "page:inbox", group: "Pages", label: "Inbox", to: INBOX_PATH, keywords: ["alerts", "notifications"] },
    { id: "page:templates", group: "Pages", label: "Templates", to: TEMPLATES_PATH, keywords: ["stack"] },
  );
  for (const section of settings) {
    for (const item of section.items) {
      entries.push({
        id: `settings:${item.to}`,
        group: "Settings",
        label: item.label,
        to: item.to,
        hint: section.title,
        keywords: ["settings", section.title],
      });
    }
  }
  if (!standalone) {
    for (const server of servers) {
      if (server.id === serverId) continue;
      entries.push({ id: `server:${server.id}`, group: "Servers", label: server.name, to: serverPath(server.id), hint: "Switch server", keywords: ["server", "switch"] });
    }
  }
  if (serverId) {
    entries.push({ id: "action:new-app", group: "Actions", label: "New app", to: newAppPath(serverId), keywords: ["create", "add", "deploy"] });
  }
  return entries;
}

function isRecentEntry(value: unknown): value is RecentEntry {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  return typeof v.id === "string" && typeof v.label === "string" && typeof v.to === "string";
}

// readRecent is the stored recent items, newest first; anything unreadable
// reads as none.
export function readRecent(): RecentEntry[] {
  try {
    const raw = window.localStorage.getItem(RECENT_KEY);
    const list: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(list) ? list.filter(isRecentEntry).slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

export function pushRecent(entry: RecentEntry): void {
  const next = [entry, ...readRecent().filter((r) => r.id !== entry.id)].slice(0, RECENT_MAX);
  try {
    window.localStorage.setItem(RECENT_KEY, JSON.stringify(next));
  } catch {
    // Storage blocked: no recent items.
  }
}

// visibleRecent keeps the recent items that still exist (an app deleted
// or on another server is gone), as they are now (a renamed app).
export function visibleRecent(recent: RecentEntry[], entries: PaletteNavEntry[]): PaletteNavEntry[] {
  const byId = new Map(entries.map((e) => [e.id, e]));
  return recent.flatMap((r) => {
    const entry = byId.get(r.id);
    return entry ? [entry] : [];
  });
}
```

Create `web/src/context/palette-context-base.ts`:

```ts
import { createContext } from "react";
import type { LucideIcon } from "lucide-react";

// PaletteAction is a command a page offers in the ⌘K palette while it is
// open (an app page's Restart, Stop, …).
export type PaletteAction = {
  id: string;
  label: string;
  icon?: LucideIcon;
  keywords?: string[];
  disabled?: boolean;
  run: () => void;
};

// PaletteActionGroup is one page's actions under a heading (the app's name).
export type PaletteActionGroup = { heading: string; actions: PaletteAction[] };

export type CommandPaletteContextValue = {
  open: boolean;
  setOpen: (open: boolean) => void;
  openPalette: () => void;
  // groups are the registered contextual actions, in registration order.
  groups: PaletteActionGroup[];
  // register adds a group under key and returns its removal.
  register: (key: string, group: PaletteActionGroup) => () => void;
};

export const CommandPaletteContext = createContext<CommandPaletteContextValue | null>(null);
```

Create `web/src/context/palette-context.tsx`:

```tsx
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";

import {
  CommandPaletteContext,
  type CommandPaletteContextValue,
  type PaletteActionGroup,
} from "@/context/palette-context-base";
import { isPaletteShortcut } from "@/lib/keyboard";

// CommandPaletteProvider owns the palette's open state (⌘K / Ctrl+K
// toggles it anywhere) and the contextual actions pages register.
export function CommandPaletteProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const [registry, setRegistry] = useState<ReadonlyMap<string, PaletteActionGroup>>(() => new Map());

  const register = useCallback((key: string, group: PaletteActionGroup) => {
    setRegistry((prev) => new Map(prev).set(key, group));
    return () =>
      setRegistry((prev) => {
        if (!prev.has(key)) return prev;
        const next = new Map(prev);
        next.delete(key);
        return next;
      });
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!isPaletteShortcut(event)) return;
      event.preventDefault();
      setOpen((o) => !o);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const openPalette = useCallback(() => setOpen(true), []);
  const groups = useMemo(() => [...registry.values()], [registry]);
  const value = useMemo<CommandPaletteContextValue>(
    () => ({ open, setOpen, openPalette, groups, register }),
    [open, openPalette, groups, register],
  );
  return <CommandPaletteContext.Provider value={value}>{children}</CommandPaletteContext.Provider>;
}
```

Create `web/src/context/use-palette.ts`:

```ts
import { useContext, useEffect, useId, useRef } from "react";

import {
  CommandPaletteContext,
  type CommandPaletteContextValue,
  type PaletteAction,
} from "@/context/palette-context-base";

const INERT: CommandPaletteContextValue = {
  open: false,
  setOpen: () => {},
  openPalette: () => {},
  groups: [],
  register: () => () => {},
};

// useCommandPalette is the palette's state; outside the shell (component
// and page tests) an inert value, so nothing needs a provider.
export function useCommandPalette(): CommandPaletteContextValue {
  return useContext(CommandPaletteContext) ?? INERT;
}

// usePaletteActions offers actions in the palette while the calling page
// is mounted. Pass a fresh array every render if convenient: the group is
// re-registered only when the heading or an action's id, label or disabled
// state changes, and every run calls the newest handler.
export function usePaletteActions(heading: string, actions: PaletteAction[]): void {
  const register = useContext(CommandPaletteContext)?.register;
  const key = useId();
  const latest = useRef(actions);
  useEffect(() => {
    latest.current = actions;
  });
  const signature = actions.map((a) => `${a.id}\u0000${a.label}\u0000${a.disabled ? 1 : 0}`).join("\u0001");

  useEffect(() => {
    if (!register) return;
    return register(key, {
      heading,
      actions: latest.current.map((action) => ({
        ...action,
        run: () => latest.current.find((a) => a.id === action.id)?.run(),
      })),
    });
  }, [register, key, heading, signature]);
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/keyboard.test.ts src/lib/palette.test.ts src/context/palette-context.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/keyboard.ts web/src/lib/keyboard.test.ts web/src/lib/palette.ts web/src/lib/palette.test.ts web/src/context/palette-context-base.ts web/src/context/palette-context.tsx web/src/context/use-palette.ts web/src/context/palette-context.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): keyboard shortcuts, palette entries and the palette action registry

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 12: The shell — no breadcrumb bar, a mobile top bar, page titles everywhere

**Files:**
- Modify (rewrite): `web/src/layouts/app-layout.tsx`
- Create: `web/src/layouts/app-layout.test.tsx`
- Delete: `web/src/layouts/use-app-layout.ts`
- Modify: `web/src/components/server-routes.tsx` (comment)
- Modify (`useAppBreadcrumbs` → `usePageTitle`): `web/src/pages/home.tsx`, `create-app.tsx`, `create-from-template.tsx`, `app-details.tsx`, `monitoring.tsx`, `org-templates.tsx`, `template-page.tsx`, `org-members.tsx`, `org-variables.tsx`, `user-password.tsx`, `user-tokens.tsx`
- Modify (drop the breadcrumb mocks/contexts): `web/src/pages/home.test.tsx`, `create-from-template.test.tsx`, `org-members.test.tsx`, `org-variables.test.tsx`, `org-templates.test.tsx`, `template-page.test.tsx`, `app-details.test.tsx`, `monitoring.test.tsx`, `web/src/components/server-links.test.tsx`

**Interfaces:**
- Consumes: `PageTitleProvider`, `useCurrentPageTitle`, `usePageTitle` (Task 3), `useIsMobile` (Task 4), `isWidePath`, `settingsPath` (Task 5), `CommandPaletteProvider`, `useCommandPalette` (Task 11), `useSidebar` (`@/components/ui/use-sidebar`).
- Produces: `AppLayout()` (same export) — wraps the shell in `PageTitleProvider` and `CommandPaletteProvider`; no header on a desktop; below 1024px a sticky top bar (`data-testid="mobile-top-bar"`: "Open menu", the page title, "Search"); content in `data-testid="page"`, capped at `max-w-[1100px]` except `isWidePath`; sidebar width 15rem (240px). `BreadcrumbEntry`, `AppLayoutOutletContext` and `useAppBreadcrumbs` no longer exist: pages name themselves with `usePageTitle(title)` or `PageHeader`.

- [ ] **Step 1: Write the failing test**

Create `web/src/layouts/app-layout.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useSidebar } from "@/components/ui/use-sidebar";
import { useCommandPalette } from "@/context/use-palette";
import { AppLayout } from "@/layouts/app-layout";
import { usePageTitle } from "@/layouts/use-page-title";
import { stubViewport } from "@/test/media";

let mustChange = false;
vi.mock("@/context/use-profile", () => ({
  useProfile: () => ({
    profile: { user_id: "u1", name: "Ann", email: "ann@example.test", must_change_password: mustChange },
  }),
}));
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ servers: [], refresh: vi.fn(), selectServer: vi.fn() }),
}));
// The sidebar has its own tests; this stand-in shows whether the phone
// sheet is open.
vi.mock("@/components/app-sidebar", () => ({
  AppSidebar: function SidebarStub() {
    return <p>{useSidebar().openMobile ? "menu open" : "menu closed"}</p>;
  },
}));
vi.mock("@/components/alerts/alert-toasts", () => ({ AlertToasts: () => null }));
vi.mock("@/components/server-creation-dialog", () => ({ ServerSelectionDialog: () => null }));

function Page({ title }: { title: string }) {
  usePageTitle(title);
  const { open } = useCommandPalette();
  return (
    <p>
      {title} body{open ? " (palette open)" : ""}
    </p>
  );
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route element={<AppLayout />}>
          <Route path="/server/s1/monitoring" element={<Page title="Monitoring" />} />
          <Route path="/settings/members" element={<Page title="Members" />} />
          <Route path="/settings/password" element={<Page title="Password" />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("AppLayout", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    mustChange = false;
    document.title = "";
  });

  it("has no breadcrumb or top bar on a desktop, and names the browser tab after the page", async () => {
    renderAt("/settings/members");
    expect(screen.getByText("Members body")).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: /breadcrumb/i })).not.toBeInTheDocument();
    expect(screen.queryByTestId("mobile-top-bar")).not.toBeInTheDocument();
    await waitFor(() => expect(document.title).toBe("Members · WinterFlow"));
  });

  it("shows a slim top bar on a phone: menu, the page's title, search", async () => {
    stubViewport(375);
    const user = userEvent.setup();
    renderAt("/server/s1/monitoring");
    const bar = screen.getByTestId("mobile-top-bar");
    expect(await within(bar).findByText("Monitoring")).toBeInTheDocument();

    await user.click(within(bar).getByRole("button", { name: "Open menu" }));
    expect(screen.getByText("menu open")).toBeInTheDocument();

    await user.click(within(bar).getByRole("button", { name: "Search" }));
    expect(screen.getByText(/palette open/)).toBeInTheDocument();
  });

  it("caps reading pages at 1100px and gives Monitoring the full width", () => {
    const { unmount } = renderAt("/settings/members");
    expect(screen.getByTestId("page")).toHaveClass("max-w-[1100px]");
    unmount();
    renderAt("/server/s1/monitoring");
    expect(screen.getByTestId("page")).not.toHaveClass("max-w-[1100px]");
  });

  it("sends a user on a temporary password to Settings → Password", () => {
    mustChange = true;
    renderAt("/settings/members");
    expect(screen.getByText("Password body")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm --dir web exec vitest run src/layouts/app-layout.test.tsx`
Expected: FAIL — no `mobile-top-bar`/`page` test ids, no palette provider, the breadcrumb header renders on every width.

- [ ] **Step 3: Implement the shell**

Replace the whole of `web/src/layouts/app-layout.tsx` with:

```tsx
import { useState, type CSSProperties } from "react"
import { Navigate, Outlet, useLocation } from "react-router-dom"
import { Menu, Search } from "lucide-react"

import { AlertToasts } from "@/components/alerts/alert-toasts"
import { AppSidebar } from "@/components/app-sidebar"
import { ServerSelectionDialog } from "@/components/server-creation-dialog"
import { Button } from "@/components/ui/button"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { useSidebar } from "@/components/ui/use-sidebar"
import { isStandalone } from "@/config"
import { CommandPaletteProvider } from "@/context/palette-context"
import { useCommandPalette } from "@/context/use-palette"
import { useProfile } from "@/context/use-profile"
import { useServers } from "@/context/use-servers"
import { useIsMobile } from "@/hooks/use-mobile"
import { PageTitleProvider } from "@/layouts/page-title"
import { useCurrentPageTitle } from "@/layouts/use-page-title"
import { isWidePath, settingsPath } from "@/lib/routes"
import { cn } from "@/lib/utils"

// The sidebar is 240px wide on a desktop (spec §2), collapsible to icons.
const SIDEBAR_STYLE = { "--sidebar-width": "15rem" } as CSSProperties

// MobileTopBar is the phone/tablet chrome (below 1024px): the menu button
// opens the sidebar sheet, the page names itself, search opens ⌘K.
function MobileTopBar() {
  const { setOpenMobile } = useSidebar()
  const { openPalette } = useCommandPalette()
  const title = useCurrentPageTitle()
  return (
    <header
      data-testid="mobile-top-bar"
      className="sticky top-0 z-20 flex h-14 shrink-0 items-center gap-1 border-b border-border/70 bg-background/70 px-2 backdrop-blur-md"
    >
      <Button variant="ghost" size="icon" className="size-10" aria-label="Open menu" onClick={() => setOpenMobile(true)}>
        <Menu className="size-5" />
      </Button>
      <p className="min-w-0 flex-1 truncate text-center text-[15px] font-semibold">{title}</p>
      <Button variant="ghost" size="icon" className="size-10" aria-label="Search" onClick={openPalette}>
        <Search className="size-5" />
      </Button>
    </header>
  )
}

// AppMain is the page area: the sky wash, no breadcrumb bar (every page
// starts with its own PageHeader), reading pages capped near 1100px.
function AppMain() {
  const isMobile = useIsMobile()
  const { pathname } = useLocation()
  return (
    <SidebarInset className="sky-wash min-w-0">
      {isMobile ? <MobileTopBar /> : null}
      <div
        data-testid="page"
        className={cn(
          "mx-auto flex w-full flex-1 flex-col gap-4 p-4 sm:p-6 lg:p-8",
          !isWidePath(pathname) && "max-w-[1100px]",
        )}
      >
        <Outlet />
      </div>
    </SidebarInset>
  )
}

export function AppLayout() {
  const { profile } = useProfile()
  const location = useLocation()
  const { refresh } = useServers()
  const [isServerDialogOpen, setIsServerDialogOpen] = useState(false)

  // Users on a temporary password are forced to set their own before doing
  // anything else.
  if (
    profile?.must_change_password &&
    location.pathname !== settingsPath("password")
  ) {
    return <Navigate to={settingsPath("password")} replace />
  }

  // Adding servers is a cloud/distributed capability; the standalone build
  // ships a single embedded server, so the trigger is omitted there.
  const handleAddServer = isStandalone
    ? undefined
    : () => setIsServerDialogOpen(true)

  return (
    <PageTitleProvider>
      <CommandPaletteProvider>
        <SidebarProvider style={SIDEBAR_STYLE}>
          <AppSidebar onAddServer={handleAddServer} />
          <AppMain />
          <AlertToasts />
          {!isStandalone ? (
            <ServerSelectionDialog
              isOpen={isServerDialogOpen}
              onClose={() => setIsServerDialogOpen(false)}
              onServerAdded={() => void refresh()}
            />
          ) : null}
        </SidebarProvider>
      </CommandPaletteProvider>
    </PageTitleProvider>
  )
}
```

Delete the breadcrumb hook:

```bash
git rm web/src/layouts/use-app-layout.ts
```

In `web/src/components/server-routes.tsx` replace the comment

```tsx
  // Hand the layout's outlet context (setBreadcrumbs) through to the pages: a
  // nested <Outlet /> without it would shadow the layout's with undefined.
```

with

```tsx
  // Hand any outlet context of the layout through to the pages: a nested
  // <Outlet /> without it would shadow the layout's with undefined.
```

- [ ] **Step 4: Migrate every page from breadcrumbs to a page title**

In each file below, replace `import { useAppBreadcrumbs } from "@/layouts/use-app-layout";` with `import { usePageTitle } from "@/layouts/use-page-title";`, delete the breadcrumbs value (the `useMemo(...)` block or the module-level `BREADCRUMBS` constant) and its `useAppBreadcrumbs(...)` call, and put the `usePageTitle` call in place of the call. Remove whatever only the breadcrumbs used — `useMemo` in most files, `toServer`/`activeServer` where nothing else reads them (`tsc -b` reports each one).

| File | Page title call |
|---|---|
| `web/src/pages/home.tsx` | `usePageTitle("Apps")` |
| `web/src/pages/create-app.tsx` | `usePageTitle("New app")` |
| `web/src/pages/create-from-template.tsx` | `usePageTitle("New app")` |
| `web/src/pages/app-details.tsx` | `usePageTitle(appId ? appLabel(app?.name, appId) : "App")` (place it where the breadcrumbs were, before the early `if (!app)` return) |
| `web/src/pages/monitoring.tsx` | `usePageTitle("Monitoring")` |
| `web/src/pages/org-templates.tsx` | `usePageTitle("Templates")` |
| `web/src/pages/template-page.tsx` | `usePageTitle(view?.name ?? (templateId ? "Template" : "Add template"))` |
| `web/src/pages/org-members.tsx` | `usePageTitle(section === "members" ? "Members" : "Organization")` |
| `web/src/pages/org-variables.tsx` | `usePageTitle("Shared variables")` |
| `web/src/pages/user-password.tsx` | `usePageTitle("Password")` |
| `web/src/pages/user-tokens.tsx` | `usePageTitle("API tokens")` |

In the tests:
- delete the line `vi.mock("@/layouts/use-app-layout", …);` from `web/src/pages/home.test.tsx`, `create-from-template.test.tsx`, `org-variables.test.tsx`, `org-templates.test.tsx`, and from `org-members.test.tsx` together with the two comment lines above it ("The page sets breadcrumbs through …");
- in `web/src/pages/template-page.test.tsx` replace

```tsx
const crumbs = vi.hoisted(() => vi.fn());
vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: crumbs }));
```

with

```tsx
const pageTitle = vi.hoisted(() => vi.fn());
vi.mock("@/layouts/use-page-title", () => ({ usePageTitle: pageTitle, useCurrentPageTitle: () => "" }));
```

and the expectation `expect(crumbs).toHaveBeenLastCalledWith([{ label: "Templates", href: "/templates" }, { label: "Add template" }]);` (as Task 10 left it) with

```tsx
    expect(pageTitle).toHaveBeenLastCalledWith("Add template");
```

- in `web/src/pages/app-details.test.tsx`, `web/src/pages/monitoring.test.tsx` and `web/src/components/server-links.test.tsx`, replace `<Outlet context={{ setBreadcrumbs: … }} />` with `<Outlet />`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/layouts src/pages src/components/server-links.test.tsx src/context/servers-context.test.tsx`
Expected: PASS

Run: `grep -rn "useAppBreadcrumbs\|use-app-layout\|setBreadcrumbs\|BreadcrumbEntry" web/src`
Expected: no output.

- [ ] **Step 6: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add -A web/src/layouts web/src/pages web/src/components/server-routes.tsx web/src/components/server-links.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): shell without breadcrumbs, a mobile top bar and page titles

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 13: The ⌘K palette, mounted in the shell, with the app page's actions

**Files:**
- Create: `web/src/components/command-palette.tsx`, `web/src/components/command-palette.test.tsx`
- Modify: `web/src/layouts/app-layout.tsx` (mount it), `web/src/layouts/app-layout.test.tsx` (stub it)
- Modify: `web/src/pages/app-details.tsx` (register Restart, Stop/Start, Pull latest images, View logs), `web/src/pages/app-details.test.tsx`

**Interfaces:**
- Consumes: `useCommandPalette`, `usePaletteActions`, `PaletteAction` (Task 11), `paletteNavEntries`, `pushRecent`, `readRecent`, `visibleRecent`, `PaletteNavEntry`, `PaletteGroup` (Task 11), `useSettingsSections` (Task 7), `useThemePreference` (Task 1), `useServers()` (`servers`, `activeServerId`), `useApps()` (`apps`), `isStandalone`.
- Produces: `CommandPalette({ onAddServer?: () => void })` (`@/components/command-palette`) — a `Dialog` (title "Search", screen-reader only) around cmdk: groups Recent (empty query only), the registered contextual groups, Apps, Pages, Settings, Servers, Actions (New app, Add server when `onAddServer`, Theme: Light/Dark/System). Selecting a destination records it as recent, closes, navigates. Phase 2 adds "Back up now" to the app page's group through `usePaletteActions`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/command-palette.test.tsx`:

```tsx
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { CommandPalette } from "@/components/command-palette";
import { CommandPaletteProvider } from "@/context/palette-context";
import { usePaletteActions } from "@/context/use-palette";
import { RECENT_KEY, readRecent } from "@/lib/palette";
import { setThemePreference } from "@/lib/theme";

const SERVERS = [
  { id: "s1", organizationId: "o", name: "Alpha", createdAt: "", lastSeenAt: null, capabilities: {}, features: { ingress: true } },
  { id: "s2", organizationId: "o", name: "Beta", createdAt: "", lastSeenAt: null, capabilities: {} },
];
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ servers: SERVERS, activeServer: SERVERS[0], activeServerId: "s1", statusByServer: {}, loading: false }),
}));
vi.mock("@/context/use-apps", () => ({
  useApps: () => ({ apps: [{ id: "a1", serverId: "s1", name: "Shop", version: "1", icon: "", color: "", createdAt: "" }] }),
}));
vi.mock("@/context/use-profile", () => ({
  useProfile: () => ({ profile: { user_id: "u1", name: "Ann", email: "ann@example.test", must_change_password: false } }),
}));

function Where() {
  const { pathname, search } = useLocation();
  return <span data-testid="at">{pathname + search}</span>;
}

function renderPalette({ onAddServer, children }: { onAddServer?: () => void; children?: ReactNode } = {}) {
  return render(
    <MemoryRouter initialEntries={["/server/s1"]}>
      <CommandPaletteProvider>
        <CommandPalette onAddServer={onAddServer} />
        {children}
        <Where />
      </CommandPaletteProvider>
    </MemoryRouter>,
  );
}

const openWithKeyboard = () => act(() => void fireEvent.keyDown(window, { key: "k", ctrlKey: true }));
const input = () => screen.findByPlaceholderText("Search apps, pages and actions…");

describe("CommandPalette", () => {
  afterEach(() => {
    localStorage.clear();
    act(() => setThemePreference("system"));
    document.documentElement.classList.remove("dark");
  });

  it("opens on Ctrl+K and jumps to a page", async () => {
    const user = userEvent.setup();
    renderPalette();
    openWithKeyboard();
    await user.type(await input(), "monitor");
    await user.click(screen.getByRole("option", { name: /Monitoring/ }));
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/monitoring");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(readRecent()[0]).toMatchObject({ id: "page:monitoring", to: "/server/s1/monitoring" });
  });

  it("finds this server's apps, the Settings sections and the other servers", async () => {
    const user = userEvent.setup();
    renderPalette();
    openWithKeyboard();
    await user.type(await input(), "shop");
    await user.click(screen.getByRole("option", { name: /Shop/ }));
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/app/a1");

    openWithKeyboard();
    await user.type(await input(), "domains");
    await user.click(screen.getByRole("option", { name: /Domains/ }));
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/settings/domains");

    openWithKeyboard();
    await user.type(await input(), "beta");
    await user.click(screen.getByRole("option", { name: /Beta/ }));
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s2");
  });

  it("shows recent items for an empty query, without the ones that are gone", async () => {
    localStorage.setItem(
      RECENT_KEY,
      JSON.stringify([
        { id: "app:gone", label: "Gone", to: "/server/s1/app/gone" },
        { id: "page:inbox", label: "Inbox", to: "/inbox" },
      ]),
    );
    const user = userEvent.setup();
    renderPalette();
    openWithKeyboard();
    const recent = await screen.findByRole("group", { name: "Recent" });
    expect(within(recent).getByRole("option", { name: /Inbox/ })).toBeInTheDocument();
    expect(within(recent).queryByText("Gone")).not.toBeInTheDocument();
    await user.type(await input(), "in");
    expect(screen.queryByRole("group", { name: "Recent" })).not.toBeInTheDocument();
  });

  it("runs the actions the open page registered", async () => {
    const restart = vi.fn();
    function AppPage() {
      usePaletteActions("Shop", [{ id: "restart", label: "Restart", run: restart }]);
      return null;
    }
    const user = userEvent.setup();
    renderPalette({ children: <AppPage /> });
    openWithKeyboard();
    const group = await screen.findByRole("group", { name: "Shop" });
    await user.click(within(group).getByRole("option", { name: /Restart/ }));
    expect(restart).toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("switches the theme", async () => {
    const user = userEvent.setup();
    renderPalette();
    openWithKeyboard();
    await user.type(await input(), "dark");
    await user.click(screen.getByRole("option", { name: /Theme: Dark/ }));
    expect(document.documentElement).toHaveClass("dark");
  });

  it("opens New app, and offers Add server only where servers can be added", async () => {
    const onAddServer = vi.fn();
    const user = userEvent.setup();
    const { unmount } = renderPalette({ onAddServer });
    openWithKeyboard();
    await user.type(await input(), "add server");
    await user.click(screen.getByRole("option", { name: /Add server/ }));
    expect(onAddServer).toHaveBeenCalled();

    openWithKeyboard();
    await user.type(await input(), "new app");
    await user.click(screen.getByRole("option", { name: /New app/ }));
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/new");
    unmount();

    renderPalette();
    openWithKeyboard();
    await input();
    expect(screen.queryByRole("option", { name: /Add server/ })).not.toBeInTheDocument();
  });
});
```

In `web/src/pages/app-details.test.tsx`:
- add the imports

```tsx
import { CommandPaletteProvider } from "@/context/palette-context";
import { useCommandPalette } from "@/context/use-palette";
```

- add, next to `NewTemplate`:

```tsx
// PaletteActions lists what the page offers in ⌘K, as buttons that run it.
function PaletteActions() {
  const { groups } = useCommandPalette();
  return (
    <ul aria-label="Palette actions">
      {groups.flatMap((g) =>
        g.actions.map((a) => (
          <li key={`${g.heading}-${a.id}`}>
            <button type="button" onClick={a.run}>{`${g.heading}: ${a.label}`}</button>
          </li>
        )),
      )}
    </ul>
  );
}
```

- in `renderPage`, wrap `<Routes>…</Routes>` in `<CommandPaletteProvider>…</CommandPaletteProvider>` and add `<PaletteActions />` next to the `<Link to="/app/a2?tab=editor">` in the route wrapper element;
- add inside `describe("AppDetailsPage", …)`:

```tsx
  it("offers its actions in the ⌘K palette", async () => {
    const user = userEvent.setup();
    const control = vi.fn(async () => {});
    renderPage("/app/a1?tab=backups", {}, { ctx: { control } });
    const list = await screen.findByRole("list", { name: "Palette actions" });
    expect(within(list).getAllByRole("button").map((b) => b.textContent)).toEqual([
      "Shop: Restart",
      "Shop: Stop",
      "Shop: Pull latest images",
      "Shop: View logs",
    ]);
    await user.click(within(list).getByRole("button", { name: "Shop: Stop" }));
    expect(control).toHaveBeenCalledWith("a1", "stop");
  });

  it("offers Start instead of Stop while the app is stopped", async () => {
    renderPage("/app/a1?tab=backups", {}, { ctx: { statusByApp: { a1: 5 } } });
    const list = await screen.findByRole("list", { name: "Palette actions" });
    expect(within(list).getByRole("button", { name: "Shop: Start" })).toBeInTheDocument();
    expect(within(list).queryByRole("button", { name: "Shop: Stop" })).not.toBeInTheDocument();
  });
```

In `web/src/layouts/app-layout.test.tsx` add `vi.mock("@/components/command-palette", () => ({ CommandPalette: () => null }));` next to the other component mocks.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/command-palette.test.tsx src/pages/app-details.test.tsx`
Expected: FAIL — `@/components/command-palette` not found; the app page registers nothing.

- [ ] **Step 3: Implement**

Create `web/src/components/command-palette.tsx`:

```tsx
import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Monitor, Moon, Server, Sun, type LucideIcon } from "lucide-react";

import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { isStandalone } from "@/config";
import { useApps } from "@/context/use-apps";
import { useCommandPalette } from "@/context/use-palette";
import { useServers } from "@/context/use-servers";
import { useSettingsSections } from "@/hooks/use-settings-sections";
import { useThemePreference } from "@/hooks/use-theme";
import {
  paletteNavEntries,
  pushRecent,
  readRecent,
  visibleRecent,
  type PaletteGroup,
  type PaletteNavEntry,
} from "@/lib/palette";
import type { ThemePreference } from "@/lib/theme";

const NAV_GROUPS: PaletteGroup[] = ["Apps", "Pages", "Settings", "Servers"];

const THEMES: { value: ThemePreference; label: string; icon: LucideIcon }[] = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
];

// The same cmdk styling the vendored CommandDialog uses.
const COMMAND_CLASS =
  "[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-muted-foreground [&_[cmdk-group]:not([hidden])_~[cmdk-group]]:pt-0 [&_[cmdk-group]]:px-2 [&_[cmdk-input-wrapper]_svg]:h-5 [&_[cmdk-input-wrapper]_svg]:w-5 [&_[cmdk-input]]:h-12 [&_[cmdk-item]]:px-2 [&_[cmdk-item]]:py-3 [&_[cmdk-item]_svg]:h-5 [&_[cmdk-item]_svg]:w-5";

// itemValue is what cmdk filters on and tells items apart by: the group,
// the label and the hint ("Settings: General (Organization)").
function itemValue(entry: PaletteNavEntry, prefix = ""): string {
  return `${prefix}${entry.group}: ${entry.label}${entry.hint ? ` (${entry.hint})` : ""}`;
}

// CommandPalette is ⌘K (spec §2): jump to an app on this server, a page, a
// Settings section or another server; New app, Add server, the theme; and
// whatever the open page registered (an app's Restart, …). An empty query
// starts with the recent destinations.
export function CommandPalette({ onAddServer }: { onAddServer?: () => void }) {
  const { open, setOpen, groups } = useCommandPalette();
  const navigate = useNavigate();
  const { servers, activeServerId } = useServers();
  const { apps } = useApps();
  const settings = useSettingsSections();
  const [, setTheme] = useThemePreference();
  const [query, setQuery] = useState("");

  const entries = useMemo(
    () => paletteNavEntries({ serverId: activeServerId, apps, servers, settings, standalone: isStandalone }),
    [activeServerId, apps, servers, settings],
  );
  // Read again on every open: another tab may have added to the list.
  const recent = useMemo(() => (open ? visibleRecent(readRecent(), entries) : []), [open, entries]);

  const close = () => {
    setOpen(false);
    setQuery("");
  };
  const go = (entry: PaletteNavEntry) => {
    pushRecent({ id: entry.id, label: entry.label, to: entry.to });
    close();
    navigate(entry.to);
  };
  const run = (action: () => void) => {
    close();
    action();
  };
  const navItem = (entry: PaletteNavEntry, prefix = "") => (
    <CommandItem key={prefix + entry.id} value={itemValue(entry, prefix)} keywords={entry.keywords} onSelect={() => go(entry)}>
      <span className="truncate">{entry.label}</span>
      {entry.hint ? <span className="ml-auto truncate text-xs text-muted-foreground">{entry.hint}</span> : null}
    </CommandItem>
  );

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
      <DialogContent className="overflow-hidden p-0 sm:max-w-xl" aria-describedby={undefined}>
        <DialogTitle className="sr-only">Search</DialogTitle>
        <Command loop className={COMMAND_CLASS}>
          <CommandInput value={query} onValueChange={setQuery} placeholder="Search apps, pages and actions…" />
          <CommandList className="max-h-[min(60vh,420px)]">
            <CommandEmpty>Nothing matches.</CommandEmpty>
            {query === "" && recent.length > 0 ? (
              <CommandGroup heading="Recent">{recent.map((entry) => navItem(entry, "Recent "))}</CommandGroup>
            ) : null}
            {groups.map((group) =>
              group.actions.length > 0 ? (
                <CommandGroup key={group.heading} heading={group.heading}>
                  {group.actions.map((action) => (
                    <CommandItem
                      key={action.id}
                      value={`${group.heading}: ${action.label}`}
                      keywords={action.keywords}
                      disabled={action.disabled}
                      onSelect={() => run(action.run)}
                    >
                      {action.icon ? <action.icon /> : null}
                      <span>{action.label}</span>
                    </CommandItem>
                  ))}
                </CommandGroup>
              ) : null,
            )}
            {NAV_GROUPS.map((name) => {
              const list = entries.filter((e) => e.group === name);
              return list.length > 0 ? (
                <CommandGroup key={name} heading={name}>
                  {list.map((entry) => navItem(entry))}
                </CommandGroup>
              ) : null;
            })}
            <CommandGroup heading="Actions">
              {entries.filter((e) => e.group === "Actions").map((entry) => navItem(entry))}
              {onAddServer ? (
                <CommandItem value="Actions: Add server" keywords={["server", "connect"]} onSelect={() => run(onAddServer)}>
                  <Server />
                  <span>Add server</span>
                </CommandItem>
              ) : null}
              {THEMES.map((theme) => (
                <CommandItem
                  key={theme.value}
                  value={`Actions: Theme: ${theme.label}`}
                  keywords={["appearance", "dark mode", "light mode"]}
                  onSelect={() => run(() => setTheme(theme.value))}
                >
                  <theme.icon />
                  <span>Theme: {theme.label}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
```

In `web/src/layouts/app-layout.tsx` add `import { CommandPalette } from "@/components/command-palette"` and render it right after `<AppMain />`:

```tsx
          <CommandPalette onAddServer={handleAddServer} />
```

In `web/src/pages/app-details.tsx`:
- add the imports `import { usePaletteActions } from "@/context/use-palette";`, `import type { PaletteAction } from "@/context/palette-context-base";`, and `ScrollText` to the `lucide-react` import (`Play`, `Square`, `RotateCw`, `ArrowUpCircle` are already imported);
- directly after the `runControl` function (before `if (!app) {`), add:

```tsx
  // ⌘K: this app's actions while its page is open, under its name. Phase 2
  // adds Back up now.
  const liveStatus = appId ? statusByApp[appId] : undefined;
  const isUp =
    liveStatus !== undefined &&
    liveStatus !== AppStatusCode.Stopped &&
    liveStatus !== AppStatusCode.Unknown;
  const paletteActions: PaletteAction[] = appId
    ? [
        { id: "restart", label: "Restart", icon: RotateCw, run: () => void runControl("restart") },
        isUp
          ? { id: "stop", label: "Stop", icon: Square, run: () => void runControl("stop") }
          : { id: "start", label: "Start", icon: Play, run: () => void runControl("start") },
        {
          id: "update",
          label: "Pull latest images",
          icon: ArrowUpCircle,
          keywords: ["update", "upgrade", "images"],
          run: () => void runControl("update"),
        },
        { id: "logs", label: "View logs", icon: ScrollText, run: () => setTab("logs") },
      ]
    : [];
  usePaletteActions(appId ? appLabel(app?.name, appId) : "App", paletteActions);
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/command-palette.test.tsx src/pages/app-details.test.tsx src/layouts/app-layout.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/command-palette.tsx web/src/components/command-palette.test.tsx web/src/layouts/app-layout.tsx web/src/layouts/app-layout.test.tsx web/src/pages/app-details.tsx web/src/pages/app-details.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): the ⌘K palette, with the open app's actions

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 14: The rebuilt sidebar — search, Apps with the live list, Monitoring, Inbox badge, Templates, Settings

**Files:**
- Modify (rewrite): `web/src/lib/nav.ts`, `web/src/lib/nav.test.ts`
- Create: `web/src/components/sidebar-link.tsx`, `web/src/components/sidebar-search.tsx`
- Modify (rewrite): `web/src/components/nav-apps.tsx`, `web/src/components/app-sidebar.tsx`
- Create: `web/src/components/app-sidebar.test.tsx`
- Delete: `web/src/components/nav-main.tsx`, `web/src/components/nav-secondary.tsx`, `web/src/components/sidebar-nav.test.tsx`
- Modify: `web/src/components/server-links.test.tsx`

**Interfaces:**
- Consumes: route builders (Task 5), `appStatus` (Task 2), `StatusDot` (Task 2), `useInboxUnread`, `unreadBadge` (Task 8), `useCommandPalette` (Task 11), `shortcutLabel` (Task 11), `useServers()` (`activeServerId`), `useApps()` (`apps`, `statusByApp`, `loading`), `useSidebar()` (`isMobile`, `setOpenMobile`), `ServerSwitcher`, `NavUser` (rebuilt in Task 15).
- Produces:
  - `@/lib/nav`: `type NavItem = { title: string; url: string; icon: LucideIcon; isActive: (pathname: string) => boolean; badge?: "inbox" }`, `type SidebarNav = { apps: NavItem; pages: NavItem[]; settings: NavItem }`, `sidebarNav(serverId: string | null): SidebarNav`, `isNavActive(pathname, url, exact = false)` (unchanged), `navItemClass` (unchanged). `navMain`, `navBottom`, `scopeNav` are removed.
  - `SidebarLink(props: ComponentProps<typeof Link>)` — a `Link` that closes the phone sheet when followed.
  - `SidebarSearch()` — the "Search… ⌘K" box that opens the palette.
  - `NavApps()` — the active server's apps (status dot, name, screen-reader status) and "New app", as a sub-list of Apps.
  - `AppSidebar({ onAddServer?: () => void })` (same signature).

- [ ] **Step 1: Write the failing tests**

Replace `web/src/lib/nav.test.ts` with:

```ts
import { describe, expect, it } from "vitest";

import { isNavActive, sidebarNav } from "@/lib/nav";

const urls = (n: ReturnType<typeof sidebarNav>) => [n.apps, ...n.pages, n.settings].map((i) => [i.title, i.url]);

describe("sidebarNav", () => {
  it("points Apps and Monitoring into the server and the rest at the org", () => {
    expect(urls(sidebarNav("s1"))).toEqual([
      ["Apps", "/server/s1"],
      ["Monitoring", "/server/s1/monitoring"],
      ["Inbox", "/inbox"],
      ["Templates", "/templates"],
      ["Settings", "/settings"],
    ]);
    expect(sidebarNav("s1").pages.find((p) => p.title === "Inbox")?.badge).toBe("inbox");
  });

  it("has no Monitoring before there is a server", () => {
    expect(urls(sidebarNav(null))).toEqual([
      ["Apps", "/"],
      ["Inbox", "/inbox"],
      ["Templates", "/templates"],
      ["Settings", "/settings"],
    ]);
  });

  it("lights Apps on the apps page only, and Settings on both settings areas", () => {
    const nav = sidebarNav("s1");
    expect(nav.apps.isActive("/server/s1")).toBe(true);
    expect(nav.apps.isActive("/server/s1/app/a1")).toBe(false);
    expect(nav.settings.isActive("/settings/members")).toBe(true);
    expect(nav.settings.isActive("/server/s1/settings/docker")).toBe(true);
    expect(nav.settings.isActive("/server/s1/monitoring")).toBe(false);
    expect(nav.pages[0].isActive("/server/s1/monitoring")).toBe(true);
  });
});

describe("isNavActive", () => {
  it("matches an exact entry only on itself", () => {
    expect(isNavActive("/server/s1", "/server/s1", true)).toBe(true);
    expect(isNavActive("/server/s1/monitoring", "/server/s1", true)).toBe(false);
  });

  it("matches other entries on themselves and their subpaths", () => {
    expect(isNavActive("/server/s1/monitoring", "/server/s1/monitoring")).toBe(true);
    expect(isNavActive("/server/s1/settings/x", "/server/s1/settings")).toBe(true);
    expect(isNavActive("/server/s1/settingsx", "/server/s1/settings")).toBe(false);
  });
});
```

Create `web/src/components/app-sidebar.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AppSidebar } from "@/components/app-sidebar";
import { TooltipProvider } from "@/components/ui/tooltip";
import { SidebarContext, type SidebarContextProps } from "@/components/ui/use-sidebar";
import { CommandPaletteProvider } from "@/context/palette-context";
import { useCommandPalette } from "@/context/use-palette";

const ALPHA = { id: "s1", organizationId: "o", name: "Alpha", createdAt: "", lastSeenAt: null, capabilities: {} };
let activeServerId: string | null = "s1";
let unread = 0;

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    servers: activeServerId ? [ALPHA] : [],
    activeServer: activeServerId ? ALPHA : null,
    activeServerId,
    selectServer: vi.fn(),
    statusByServer: {},
    loading: false,
    refresh: vi.fn(),
  }),
}));
vi.mock("@/context/use-apps", () => ({
  useApps: () => ({
    apps: [{ id: "a1", serverId: "s1", name: "Shop", version: "1", icon: "", color: "", createdAt: "" }],
    statusByApp: { a1: 1 },
    loading: false,
  }),
}));
vi.mock("@/hooks/use-inbox-unread", () => ({
  useInboxUnread: () => ({ count: unread, seenAt: 0, isUnread: () => false, markAllRead: () => {} }),
}));
// NavUser needs the account contexts; it has its own test.
vi.mock("@/components/nav-user", () => ({ NavUser: () => null }));

const setOpenMobile = vi.fn();
function sidebarState(isMobile: boolean): SidebarContextProps {
  return {
    state: "expanded",
    open: true,
    setOpen: () => {},
    openMobile: isMobile,
    setOpenMobile,
    isMobile,
    toggleSidebar: () => {},
  };
}

function PaletteState() {
  return <span data-testid="palette">{useCommandPalette().open ? "open" : "closed"}</span>;
}

function renderAt(url: string, isMobile = false) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <CommandPaletteProvider>
        <TooltipProvider>
          <SidebarContext.Provider value={sidebarState(isMobile)}>
            <AppSidebar />
            <PaletteState />
          </SidebarContext.Provider>
        </TooltipProvider>
      </CommandPaletteProvider>
    </MemoryRouter>,
  );
}

const lit = () =>
  screen
    .getAllByRole("link")
    .filter((a) => a.getAttribute("data-active") === "true")
    .map((a) => a.getAttribute("href"));

describe("AppSidebar", () => {
  afterEach(() => {
    activeServerId = "s1";
    unread = 0;
    setOpenMobile.mockClear();
  });

  it("lists Apps with the live app list and New app, then Monitoring, Inbox, Templates, and Settings", () => {
    renderAt("/server/s1");
    expect(screen.getAllByRole("link").map((a) => a.getAttribute("href"))).toEqual([
      "/server/s1",
      "/server/s1/app/a1",
      "/server/s1/new",
      "/server/s1/monitoring",
      "/inbox",
      "/templates",
      "/settings",
    ]);
    expect(screen.getByRole("link", { name: "Shop, Running" })).toBeInTheDocument();
    expect(screen.queryByText("Dashboard")).not.toBeInTheDocument();
  });

  it.each([
    ["/server/s1", ["/server/s1"]],
    ["/server/s1/app/a1", ["/server/s1/app/a1"]],
    ["/server/s1/new", ["/server/s1/new"]],
    ["/server/s1/monitoring", ["/server/s1/monitoring"]],
    ["/inbox", ["/inbox"]],
    ["/templates/t1", ["/templates"]],
    ["/settings/members", ["/settings"]],
    ["/server/s1/settings/docker", ["/settings"]],
  ])("lights only the current entry at %s", (url, expected) => {
    renderAt(url);
    expect(lit()).toEqual(expected);
  });

  it("counts unread Inbox items, up to 99+", () => {
    unread = 3;
    const { unmount } = renderAt("/server/s1");
    expect(screen.getByLabelText("3 unread")).toHaveTextContent("3");
    unmount();
    unread = 150;
    renderAt("/server/s1");
    expect(screen.getByLabelText("150 unread")).toHaveTextContent("99+");
  });

  it("shows no badge when nothing is unread", () => {
    renderAt("/server/s1");
    expect(screen.queryByLabelText(/unread/)).not.toBeInTheDocument();
  });

  it("opens the palette from the search box", async () => {
    renderAt("/server/s1");
    await userEvent.setup().click(screen.getByRole("button", { name: /Search/ }));
    expect(screen.getByTestId("palette")).toHaveTextContent("open");
  });

  it("has no server pages before there is a server", () => {
    activeServerId = null;
    renderAt("/");
    expect(screen.getByRole("link", { name: "Apps" })).toHaveAttribute("href", "/");
    expect(screen.queryByRole("link", { name: "Monitoring" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /New app/ })).not.toBeInTheDocument();
  });

  it("closes the phone sheet when a link is followed", async () => {
    renderAt("/server/s1", true);
    await userEvent.setup().click(screen.getByRole("link", { name: "Templates" }));
    expect(setOpenMobile).toHaveBeenCalledWith(false);
  });
});
```

In `web/src/components/server-links.test.tsx`:
- add `vi.mock("@/hooks/use-inbox-unread", () => ({ useInboxUnread: () => ({ count: 0, seenAt: 0, isUnread: () => false, markAllRead: () => {} }) }));`;
- in "points the sidebar and the app grid into the active server" replace the Dashboard and Server Settings expectations with:

```tsx
    expect(screen.getByRole("link", { name: "Apps" })).toHaveAttribute("href", "/server/s1");
    expect(screen.getByRole("link", { name: "Settings" })).toHaveAttribute("href", "/settings");
```

(the Monitoring expectation stays).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/nav.test.ts src/components/app-sidebar.test.tsx src/components/server-links.test.tsx`
Expected: FAIL — `sidebarNav` is not exported; the sidebar still shows Dashboard / Server Settings.

- [ ] **Step 3: Implement**

Replace the whole of `web/src/lib/nav.ts` with:

```ts
import { Activity, Inbox, LayoutGrid, LayoutTemplate, Settings, type LucideIcon } from "lucide-react";

import { INBOX_PATH, SETTINGS_PATH, TEMPLATES_PATH, appsPath, monitoringPath } from "@/lib/routes";
import { serverPath } from "@/lib/server-paths";

export type NavItem = {
  title: string;
  url: string;
  icon: LucideIcon;
  isActive: (pathname: string) => boolean;
  // badge names the count shown beside the entry.
  badge?: "inbox";
};

// SidebarNav is the sidebar (spec §2): Apps (with the live app list under
// it), the pages, and Settings pinned above the account footer.
export type SidebarNav = { apps: NavItem; pages: NavItem[]; settings: NavItem };

// sidebarNav builds the sidebar for the active server: Apps and Monitoring
// are the server's, Inbox, Templates and Settings the organization's
// (Settings also lights on the server's own settings pages).
export function sidebarNav(serverId: string | null): SidebarNav {
  const apps = serverId ? appsPath(serverId) : "/";
  const pages: NavItem[] = [];
  if (serverId) {
    const monitoring = monitoringPath(serverId);
    pages.push({ title: "Monitoring", url: monitoring, icon: Activity, isActive: (p) => isNavActive(p, monitoring) });
  }
  pages.push(
    { title: "Inbox", url: INBOX_PATH, icon: Inbox, badge: "inbox", isActive: (p) => isNavActive(p, INBOX_PATH) },
    { title: "Templates", url: TEMPLATES_PATH, icon: LayoutTemplate, isActive: (p) => isNavActive(p, TEMPLATES_PATH) },
  );
  const serverSettings = serverId ? serverPath(serverId, "/settings") : null;
  return {
    apps: { title: "Apps", url: apps, icon: LayoutGrid, isActive: (p) => p === apps },
    pages,
    settings: {
      title: "Settings",
      url: SETTINGS_PATH,
      icon: Settings,
      isActive: (p) => isNavActive(p, SETTINGS_PATH) || (serverSettings !== null && isNavActive(p, serverSettings)),
    },
  };
}

// isNavActive reports whether a sidebar link matches the current path: an
// exact entry only on itself, any other entry on itself or a subpath.
export function isNavActive(pathname: string, url: string, exact = false): boolean {
  if (exact) return pathname === url;
  return pathname === url || pathname.startsWith(`${url}/`);
}

// The active sidebar entry is a pill (the sidebar accent) with its icon lit
// in the sidebar's sky blue, as in twillingate's shell.
export const navItemClass = "data-[active=true]:[&>svg]:text-sidebar-primary";
```

Create `web/src/components/sidebar-link.tsx`:

```tsx
import type { ComponentProps } from "react";
import { Link } from "react-router-dom";

import { useSidebar } from "@/components/ui/use-sidebar";

// SidebarLink is a router link that closes the phone sidebar sheet once it
// is followed (the sheet would otherwise stay over the new page).
export function SidebarLink({ onClick, ...props }: ComponentProps<typeof Link>) {
  const { isMobile, setOpenMobile } = useSidebar();
  return (
    <Link
      {...props}
      onClick={(event) => {
        onClick?.(event);
        if (isMobile) setOpenMobile(false);
      }}
    />
  );
}
```

Create `web/src/components/sidebar-search.tsx`:

```tsx
import { Search } from "lucide-react";

import { Kbd } from "@/components/ui/kbd";
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar";
import { useSidebar } from "@/components/ui/use-sidebar";
import { useCommandPalette } from "@/context/use-palette";
import { shortcutLabel } from "@/lib/keyboard";

// SidebarSearch is the sidebar's search box: it opens the ⌘K palette (and
// closes the phone sheet first).
export function SidebarSearch() {
  const { openPalette } = useCommandPalette();
  const { isMobile, setOpenMobile } = useSidebar();
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <SidebarMenuButton
          tooltip="Search"
          aria-keyshortcuts="Meta+K Control+K"
          className="border border-sidebar-border bg-background/60 text-muted-foreground max-lg:h-10"
          onClick={() => {
            if (isMobile) setOpenMobile(false);
            openPalette();
          }}
        >
          <Search />
          <span>Search…</span>
          <Kbd className="ml-auto group-data-[collapsible=icon]:hidden">{shortcutLabel("K")}</Kbd>
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
```

Replace the whole of `web/src/components/nav-apps.tsx` with:

```tsx
import { useLocation } from "react-router-dom";
import { Plus } from "lucide-react";

import { SidebarLink } from "@/components/sidebar-link";
import { StatusDot } from "@/components/status";
import { SidebarMenuSub, SidebarMenuSubButton, SidebarMenuSubItem } from "@/components/ui/sidebar";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { appLabel } from "@/lib/kpi";
import { appPath, newAppPath } from "@/lib/routes";
import { appStatus } from "@/lib/status";
import { cn } from "@/lib/utils";

// NavApps is the live app list under Apps: each app of the active server
// with its status dot, then New app. Hidden when the sidebar is collapsed
// to icons.
export function NavApps() {
  const { apps, statusByApp, loading } = useApps();
  const { activeServerId } = useServers();
  const { pathname } = useLocation();
  if (!activeServerId) return null;
  const newApp = newAppPath(activeServerId);

  return (
    <SidebarMenuSub>
      {loading && apps.length === 0 ? (
        <SidebarMenuSubItem>
          <span className="px-2 text-xs text-muted-foreground">Loading…</span>
        </SidebarMenuSubItem>
      ) : null}
      {apps.map((app) => {
        const to = appPath(activeServerId, app.id);
        const status = appStatus(statusByApp[app.id]);
        return (
          <SidebarMenuSubItem key={app.id}>
            <SidebarMenuSubButton asChild isActive={pathname === to} className="max-lg:h-10">
              <SidebarLink to={to}>
                <StatusDot tone={status.tone} pulse={status.pulse} />
                {/* Not reconciled yet: the id prefix stands in for the name. */}
                <span className={cn("truncate", !app.name?.trim() && "font-mono text-muted-foreground")}>
                  {appLabel(app.name, app.id)}
                  <span className="sr-only">, {status.label}</span>
                </span>
              </SidebarLink>
            </SidebarMenuSubButton>
          </SidebarMenuSubItem>
        );
      })}
      <SidebarMenuSubItem>
        <SidebarMenuSubButton asChild isActive={pathname === newApp} className="text-muted-foreground max-lg:h-10">
          <SidebarLink to={newApp}>
            <Plus />
            <span>New app</span>
          </SidebarLink>
        </SidebarMenuSubButton>
      </SidebarMenuSubItem>
    </SidebarMenuSub>
  );
}
```

Replace the whole of `web/src/components/app-sidebar.tsx` with:

```tsx
import { useMemo, type ReactNode } from "react";
import { useLocation } from "react-router-dom";

import { NavApps } from "@/components/nav-apps";
import { NavUser } from "@/components/nav-user";
import { ServerSwitcher } from "@/components/server-switcher";
import { SidebarLink } from "@/components/sidebar-link";
import { SidebarSearch } from "@/components/sidebar-search";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "@/components/ui/sidebar";
import { useServers } from "@/context/use-servers";
import { useInboxUnread } from "@/hooks/use-inbox-unread";
import { unreadBadge } from "@/lib/inbox";
import { navItemClass, sidebarNav, type NavItem } from "@/lib/nav";
import { cn } from "@/lib/utils";

// AppSidebar is the shell's navigation (spec §2): the server switcher and
// search on top; Apps with the live app list, Monitoring, Inbox (unread
// badge), Templates; Settings above the account footer. Collapsible to
// icons on a desktop, a sheet below 1024px.
export function AppSidebar({ onAddServer }: { onAddServer?: () => void }) {
  const { activeServerId } = useServers();
  const { pathname } = useLocation();
  const { count } = useInboxUnread();
  const nav = useMemo(() => sidebarNav(activeServerId), [activeServerId]);

  const entry = (item: NavItem, extra?: ReactNode) => (
    <SidebarMenuItem key={item.title}>
      <SidebarMenuButton
        asChild
        tooltip={item.title}
        isActive={item.isActive(pathname)}
        className={cn(navItemClass, "max-lg:h-10")}
      >
        <SidebarLink to={item.url}>
          <item.icon />
          <span>{item.title}</span>
        </SidebarLink>
      </SidebarMenuButton>
      {extra}
    </SidebarMenuItem>
  );

  const inboxBadge =
    count > 0 ? (
      <SidebarMenuBadge
        aria-label={`${count} unread`}
        className="rounded-full bg-sidebar-primary text-sidebar-primary-foreground peer-hover/menu-button:text-sidebar-primary-foreground peer-data-[active=true]/menu-button:text-sidebar-primary-foreground"
      >
        {unreadBadge(count)}
      </SidebarMenuBadge>
    ) : null;

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <ServerSwitcher onAddServer={onAddServer} />
        <SidebarSearch />
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarMenu>
            {entry(nav.apps, <NavApps />)}
            {nav.pages.map((item) => entry(item, item.badge === "inbox" ? inboxBadge : null))}
          </SidebarMenu>
        </SidebarGroup>
        <SidebarGroup className="mt-auto border-t border-sidebar-border">
          <SidebarMenu>{entry(nav.settings)}</SidebarMenu>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <NavUser />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  );
}
```

Delete the old navigation pieces:

```bash
git rm web/src/components/nav-main.tsx web/src/components/nav-secondary.tsx web/src/components/sidebar-nav.test.tsx
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/nav.test.ts src/components/app-sidebar.test.tsx src/components/server-links.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add -A web/src/lib/nav.ts web/src/lib/nav.test.ts web/src/components/sidebar-link.tsx web/src/components/sidebar-search.tsx web/src/components/nav-apps.tsx web/src/components/app-sidebar.tsx web/src/components/app-sidebar.test.tsx web/src/components/nav-main.tsx web/src/components/nav-secondary.tsx web/src/components/sidebar-nav.test.tsx web/src/components/server-links.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): rebuilt sidebar — search, live app list, Inbox badge, Settings

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 15: Server switcher with status and ⌘1…⌘9, and the user menu with the theme

**Files:**
- Modify (rewrite): `web/src/components/server-switcher.tsx`
- Create: `web/src/components/server-switcher.test.tsx`
- Create: `web/src/hooks/use-server-shortcuts.ts`, `web/src/hooks/use-server-shortcuts.test.tsx`
- Modify: `web/src/layouts/app-layout.tsx` (wire the shortcuts)
- Modify (rewrite): `web/src/components/nav-user.tsx`, `web/src/components/nav-user.test.tsx`

**Interfaces:**
- Consumes: `serverStatus` (Task 2), `StatusDot` (Task 2), `serverShortcutIndex`, `shortcutLabel` (Task 11), `useThemePreference` (Task 1), `useServers()` (`servers`, `activeServer`, `selectServer`, `statusByServer`, `loading`), `useAuth()` (`logout`), `useUser()` (`user`, `loading`), `isStandalone`.
- Produces:
  - `ServerSwitcher({ onAddServer?: () => void })` (same signature) — distributed: a dropdown of servers with status dots, the current one checked, `⌘1…⌘9` labels, "Add server"; standalone: logo + server name, no chevron, no menu (not a button).
  - `useServerShortcuts(enabled: boolean): void` (`@/hooks/use-server-shortcuts`) — ⌘/Ctrl+1…9 switch to that server through `selectServer` (which keeps the page kind).
  - `NavUser()` (same export) — the account footer: avatar + name; menu with Theme (Light / Dark / System radio items) and Log out. The organization and account pages moved to Settings.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/server-switcher.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ServerSwitcher } from "@/components/server-switcher";
import { SidebarContext, type SidebarContextProps } from "@/components/ui/use-sidebar";
import type { Server, ServerLiveness } from "@/context/servers-context-base";

let standalone = false;
vi.mock("@/config", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/config")>()),
  get isStandalone() {
    return standalone;
  },
}));

const server = (id: string, name: string, lastSeenAt: string | null = null): Server => ({
  id,
  organizationId: "o",
  name,
  createdAt: "",
  lastSeenAt,
  capabilities: {},
});
let servers: Server[] = [
  server("s1", "Alpha", "2026-10-13T10:00:00Z"),
  server("s2", "Beta"),
  server("s3", "Gamma", "2026-10-01T10:00:00Z"),
];
let statusByServer: Record<string, ServerLiveness> = { s1: "online", s3: "unknown" };
const selectServer = vi.fn();

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    servers,
    activeServer: servers[0] ?? null,
    activeServerId: servers[0]?.id ?? null,
    selectServer,
    statusByServer,
    loading: false,
  }),
}));

const sidebar = { isMobile: false, state: "expanded" } as SidebarContextProps;

function renderSwitcher(onAddServer = vi.fn()) {
  render(
    <SidebarContext.Provider value={sidebar}>
      <ServerSwitcher onAddServer={onAddServer} />
    </SidebarContext.Provider>,
  );
  return onAddServer;
}

describe("ServerSwitcher", () => {
  afterEach(() => {
    standalone = false;
    servers = [server("s1", "Alpha", "2026-10-13T10:00:00Z"), server("s2", "Beta"), server("s3", "Gamma", "2026-10-01T10:00:00Z")];
    statusByServer = { s1: "online", s3: "unknown" };
    selectServer.mockClear();
  });

  it("lists the servers with their status and a shortcut each, and switches", async () => {
    const user = userEvent.setup();
    renderSwitcher();
    const trigger = screen.getByRole("button", { name: /Alpha/ });
    expect(trigger).toHaveTextContent("Online");
    await user.click(trigger);
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByRole("img", { name: "Online" })).toBeInTheDocument();
    expect(within(menu).getByRole("img", { name: "Never connected" })).toBeInTheDocument();
    expect(within(menu).getByRole("img", { name: "Offline" })).toBeInTheDocument();
    expect(within(menu).getByLabelText("Current server")).toBeInTheDocument();
    expect(within(menu).getByText("Ctrl+2")).toBeInTheDocument();
    await user.click(within(menu).getByText("Beta"));
    expect(selectServer).toHaveBeenCalledWith("s2");
  });

  it("labels only the first nine servers with a shortcut", async () => {
    servers = Array.from({ length: 10 }, (_, i) => server(`s${i + 1}`, `Server ${i + 1}`));
    const user = userEvent.setup();
    renderSwitcher();
    await user.click(screen.getByRole("button", { name: /Server 1/ }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getAllByText(/^Ctrl\+\d$/)).toHaveLength(9);
  });

  it("adds a server from the menu", async () => {
    const user = userEvent.setup();
    const onAddServer = renderSwitcher();
    await user.click(screen.getByRole("button", { name: /Alpha/ }));
    await user.click(within(await screen.findByRole("menu")).getByText("Add server"));
    expect(onAddServer).toHaveBeenCalled();
  });

  it("offers adding the first server when there is none", async () => {
    servers = [];
    const user = userEvent.setup();
    const onAddServer = renderSwitcher();
    await user.click(screen.getByRole("button", { name: /Add a server/ }));
    expect(onAddServer).toHaveBeenCalled();
  });

  it("is just the server's name on a standalone install", () => {
    standalone = true;
    renderSwitcher();
    expect(screen.getByText("Alpha")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.queryByText("Online")).not.toBeInTheDocument();
  });
});
```

Create `web/src/hooks/use-server-shortcuts.test.tsx`:

```tsx
import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useServerShortcuts } from "@/hooks/use-server-shortcuts";

const selectServer = vi.fn();
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ servers: [{ id: "s1" }, { id: "s2" }], selectServer }),
}));

function Probe({ enabled }: { enabled: boolean }) {
  useServerShortcuts(enabled);
  return null;
}

const press = (init: KeyboardEventInit) => {
  const event = new KeyboardEvent("keydown", { cancelable: true, ...init });
  window.dispatchEvent(event);
  return event;
};

describe("useServerShortcuts", () => {
  afterEach(() => selectServer.mockClear());

  it("switches to the Nth server on ⌘N or Ctrl+N, instead of the browser's tab switch", () => {
    render(<Probe enabled />);
    expect(press({ key: "2", ctrlKey: true }).defaultPrevented).toBe(true);
    expect(selectServer).toHaveBeenLastCalledWith("s2");
    press({ key: "1", metaKey: true });
    expect(selectServer).toHaveBeenLastCalledWith("s1");
  });

  it("leaves numbers past the last server and other modifiers to the browser", () => {
    render(<Probe enabled />);
    expect(press({ key: "3", ctrlKey: true }).defaultPrevented).toBe(false);
    press({ key: "1", ctrlKey: true, altKey: true });
    press({ key: "1" });
    expect(selectServer).not.toHaveBeenCalled();
  });

  it("does nothing when disabled (a standalone install)", () => {
    render(<Probe enabled={false} />);
    press({ key: "1", ctrlKey: true });
    expect(selectServer).not.toHaveBeenCalled();
  });
});
```

Replace `web/src/components/nav-user.test.tsx` with:

```tsx
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NavUser } from "@/components/nav-user";
import { SidebarContext, type SidebarContextProps } from "@/components/ui/use-sidebar";
import { AuthContext, type AuthContextValue } from "@/context/auth-context-base";
import { UserContext, type UserContextValue } from "@/context/user-context-base";
import { THEME_KEY, setThemePreference } from "@/lib/theme";

function renderNav() {
  const sidebar = { isMobile: false, state: "expanded" } as SidebarContextProps;
  const logout = vi.fn();
  const auth = { logout } as unknown as AuthContextValue;
  const user = {
    user: { id: "u-1", name: "Ann", picture: "", provider: "local" },
    loading: false,
  } as UserContextValue;
  render(
    <MemoryRouter>
      <SidebarContext.Provider value={sidebar}>
        <AuthContext.Provider value={auth}>
          <UserContext.Provider value={user}>
            <NavUser />
          </UserContext.Provider>
        </AuthContext.Provider>
      </SidebarContext.Provider>
    </MemoryRouter>,
  );
  return { logout };
}

describe("NavUser", () => {
  afterEach(() => {
    act(() => setThemePreference("system"));
    localStorage.clear();
    document.documentElement.classList.remove("dark");
  });

  it("switches the theme between Light, Dark and System", async () => {
    const user = userEvent.setup();
    renderNav();
    await user.click(screen.getByRole("button", { name: /Ann/ }));
    expect(await screen.findByRole("menuitemradio", { name: "System" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("menuitemradio", { name: "Light" })).toHaveAttribute("aria-checked", "false");
    await user.click(screen.getByRole("menuitemradio", { name: "Dark" }));
    expect(document.documentElement).toHaveClass("dark");
    expect(localStorage.getItem(THEME_KEY)).toBe("dark");
  });

  it("logs out", async () => {
    const user = userEvent.setup();
    const { logout } = renderNav();
    await user.click(screen.getByRole("button", { name: /Ann/ }));
    await user.click(await screen.findByRole("menuitem", { name: /log ?out/i }));
    expect(logout).toHaveBeenCalled();
  });

  it("no longer carries the organization's pages (they live in Settings)", async () => {
    const user = userEvent.setup();
    renderNav();
    await user.click(screen.getByRole("button", { name: /Ann/ }));
    await screen.findByRole("menu");
    for (const name of ["Members", "Alerts", "Org variables", "Templates", "API tokens", "Change password"]) {
      expect(screen.queryByRole("menuitem", { name })).not.toBeInTheDocument();
    }
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/server-switcher.test.tsx src/hooks/use-server-shortcuts.test.tsx src/components/nav-user.test.tsx`
Expected: FAIL — no status in the switcher, no shortcuts hook, the user menu still lists the org pages and has no theme.

- [ ] **Step 3: Implement**

Replace the whole of `web/src/components/server-switcher.tsx` with:

```tsx
import { Check, ChevronsUpDown, Plus } from "lucide-react";

import { LogoSpinner } from "@/components/app-logo";
import { StatusDot } from "@/components/status";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar";
import { useSidebar } from "@/components/ui/use-sidebar";
import { isStandalone } from "@/config";
import type { Server } from "@/context/servers-context-base";
import { useServers } from "@/context/use-servers";
import { shortcutLabel } from "@/lib/keyboard";
import { serverStatus } from "@/lib/status";

const LOGO_BOX =
  "bg-white text-sidebar-primary-soft flex aspect-square size-8 items-center justify-center rounded-lg border border-sidebar-border/50";

function Logo() {
  return <LogoSpinner size="md" containerClassName={LOGO_BOX} iconClassName="text-sidebar-primary-soft" />;
}

// placeholderRow keeps the header stable (icon + two lines) while servers
// load or before the first one exists.
function placeholderRow(title: string, subtitle: string) {
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <SidebarMenuButton size="lg">
          <Logo />
          <div className="grid flex-1 text-left text-sm leading-tight">
            <span className="truncate font-medium">{title}</span>
            <span className="truncate text-xs text-muted-foreground">{subtitle}</span>
          </div>
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}

// ServerSwitcher heads the sidebar (spec §2). Distributed: the active
// server with its status, a menu of every server (status dot, ⌘1…⌘9) and
// Add server. Standalone: the one embedded server's name, no menu.
export function ServerSwitcher({ onAddServer }: { onAddServer?: () => void }) {
  const { isMobile } = useSidebar();
  const { servers, activeServer, selectServer, statusByServer, loading } = useServers();

  if (!activeServer) {
    if (loading) return placeholderRow("WinterFlow", "Loading…");
    if (isStandalone) return placeholderRow("WinterFlow", "This server");
    return (
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton size="lg" onClick={() => onAddServer?.()} className="cursor-pointer">
            <div className="flex aspect-square size-8 items-center justify-center rounded-lg border border-sidebar-border bg-sidebar-accent">
              <Plus className="size-4" />
            </div>
            <div className="grid flex-1 text-left text-sm leading-tight">
              <span className="truncate font-medium">Add a server</span>
              <span className="truncate text-xs text-muted-foreground">No servers yet</span>
            </div>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    );
  }

  if (isStandalone) {
    return (
      <SidebarMenu>
        <SidebarMenuItem>
          <div className="flex h-12 items-center gap-2 rounded-md p-2 text-sm group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:p-0">
            <Logo />
            <span className="truncate font-medium group-data-[collapsible=icon]:hidden">{activeServer.name}</span>
          </div>
        </SidebarMenuItem>
      </SidebarMenu>
    );
  }

  const statusOf = (s: Server) =>
    serverStatus({ liveness: statusByServer?.[s.id], lastSeenAt: s.lastSeenAt, standalone: false });
  const current = statusOf(activeServer);

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              <Logo />
              <div className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-medium">{activeServer.name}</span>
                <span className="flex items-center gap-1.5 truncate text-xs text-muted-foreground">
                  <StatusDot tone={current.tone} pulse={current.pulse} />
                  {current.label}
                </span>
              </div>
              <ChevronsUpDown className="ml-auto" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            className="w-(--radix-dropdown-menu-trigger-width) min-w-56 rounded-lg"
            align="start"
            side={isMobile ? "bottom" : "right"}
            sideOffset={4}
          >
            <DropdownMenuLabel className="text-xs text-muted-foreground">Servers</DropdownMenuLabel>
            {servers.map((s, index) => {
              const status = statusOf(s);
              return (
                <DropdownMenuItem key={s.id} onClick={() => selectServer(s.id)} className="min-h-10 gap-2 p-2">
                  <StatusDot tone={status.tone} pulse={status.pulse} label={status.label} />
                  <span className="truncate">{s.name}</span>
                  {s.id === activeServer.id ? <Check className="size-4" aria-label="Current server" /> : null}
                  {index < 9 ? <DropdownMenuShortcut>{shortcutLabel(index + 1)}</DropdownMenuShortcut> : null}
                </DropdownMenuItem>
              );
            })}
            <DropdownMenuSeparator />
            <DropdownMenuItem className="min-h-10 gap-2 p-2" onClick={() => onAddServer?.()}>
              <div className="flex size-6 items-center justify-center rounded-md border bg-transparent">
                <Plus className="size-4" />
              </div>
              <div className="font-medium text-muted-foreground">Add server</div>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
```

Create `web/src/hooks/use-server-shortcuts.ts`:

```ts
import { useEffect } from "react";

import { useServers } from "@/context/use-servers";
import { serverShortcutIndex } from "@/lib/keyboard";

// useServerShortcuts wires ⌘1…⌘9 (Ctrl elsewhere) to the switcher's
// servers: the page kind is kept (selectServer). Keys past the last server
// stay the browser's. Off on a standalone install (one server).
export function useServerShortcuts(enabled: boolean): void {
  const { servers, selectServer } = useServers();
  useEffect(() => {
    if (!enabled) return;
    const onKeyDown = (event: KeyboardEvent) => {
      const index = serverShortcutIndex(event);
      if (index === null || index >= servers.length) return;
      event.preventDefault();
      selectServer(servers[index].id);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [enabled, servers, selectServer]);
}
```

In `web/src/layouts/app-layout.tsx` add `import { useServerShortcuts } from "@/hooks/use-server-shortcuts"` and call it in `AppLayout` right after `const [isServerDialogOpen, setIsServerDialogOpen] = useState(false)` (before the early return, so the hook order never changes):

```tsx
  useServerShortcuts(!isStandalone)
```

Replace the whole of `web/src/components/nav-user.tsx` with:

```tsx
import { ChevronsUpDown, LogOut, Monitor, Moon, Sun } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar";
import { useSidebar } from "@/components/ui/use-sidebar";
import { useAuth } from "@/context/use-auth";
import { useUser } from "@/context/use-user";
import { useThemePreference } from "@/hooks/use-theme";
import type { ThemePreference } from "@/lib/theme";

const THEMES: { value: ThemePreference; label: string; icon: typeof Sun }[] = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
];

// NavUser is the account footer of the sidebar: who is signed in, the
// theme, Log out. The organization's and the account's pages are in
// Settings.
export function NavUser() {
  const { isMobile } = useSidebar();
  const { logout } = useAuth();
  const { user, loading } = useUser();
  const [theme, setTheme] = useThemePreference();
  const { t } = useTranslation();
  const displayName = user?.name ?? (loading ? "Loading..." : "Unknown user");
  const avatarFallback = user?.name?.slice(0, 2).toUpperCase() ?? "WF";

  const identity = (
    <>
      <Avatar className="h-8 w-8 rounded-lg">
        <AvatarImage src={user?.picture} alt={displayName} />
        <AvatarFallback className="rounded-lg">{avatarFallback}</AvatarFallback>
      </Avatar>
      <div className="grid flex-1 text-left text-sm leading-tight">
        <span className="truncate font-medium">{displayName}</span>
      </div>
    </>
  );

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              {identity}
              <ChevronsUpDown className="ml-auto size-4" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            className="w-(--radix-dropdown-menu-trigger-width) min-w-56 rounded-lg"
            side={isMobile ? "bottom" : "right"}
            align="end"
            sideOffset={4}
          >
            <DropdownMenuLabel className="p-0 font-normal">
              <div className="flex items-center gap-2 px-1 py-1.5 text-left text-sm">{identity}</div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">Theme</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as ThemePreference)}>
              {THEMES.map((item) => (
                <DropdownMenuRadioItem key={item.value} value={item.value} className="min-h-10 gap-2">
                  <item.icon className="size-4" aria-hidden />
                  {item.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="min-h-10" onClick={() => logout()}>
              <LogOut />
              {t("auth.logout")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/server-switcher.test.tsx src/hooks/use-server-shortcuts.test.tsx src/components/nav-user.test.tsx src/components/server-links.test.tsx src/layouts/app-layout.test.tsx`
Expected: PASS

Run the old-address check from Task 10 again, without the `nav-user` exclusion:

```bash
grep -rnE '/org/(members|alerts|variables|templates)|/user/(tokens|password)|"/create-app|settings\?tab=' web/src --include=*.tsx --include=*.ts \
  | grep -v -e 'src/routes.tsx' -e 'src/routes.test.tsx' -e 'lib/routes.test.ts' -e '^[^:]*:[0-9]*: *//'
```

Expected: no output.

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/server-switcher.tsx web/src/components/server-switcher.test.tsx web/src/hooks/use-server-shortcuts.ts web/src/hooks/use-server-shortcuts.test.tsx web/src/layouts/app-layout.tsx web/src/components/nav-user.tsx web/src/components/nav-user.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): server switcher with status and ⌘1–⌘9, user menu with the theme

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 16: Phase verification

**Files:** none changed unless a check fails (then fix in the task's files and commit as `fix(web): …`).

- [ ] **Step 1: The whole web suite, lint, types, build**

```bash
pnpm --dir web run lint
pnpm --dir web exec tsc -b
pnpm --dir web run test:coverage
pnpm --dir web run build && git checkout -- web/dist/.gitkeep
```

Expected: all pass; `git status --short web/dist` prints nothing.

- [ ] **Step 2: Diff coverage of the changed web lines**

```bash
go run ./tools/diffcov --base main --min 80 --lcov web/coverage/lcov.info
```

Expected: web changed lines ≥ 80% covered. If a file falls short, add the missing test to that task's test file (the uncovered lines are listed), commit, re-run.

- [ ] **Step 3: Nothing outside `web/` and `docs/` changed**

```bash
git diff --stat main -- . ':!web' ':!docs'
```

Expected: no output (no Go, no `api/openapi.yaml`).

- [ ] **Step 4: Browser check on a throwaway standalone instance**

Build and run a standalone binary against a scratch data directory (as in `README`/`.env.example`: `make build`, then `bin/standalone` with `DATABASE_URL=sqlite://…` and `AGENT_DATA_DIR` in a temp dir), sign in, and check at 1280, 768 and 375 px, light and dark:
- 1280: sidebar 240px with switcher (standalone: name only), Search…, Apps + app list + New app, Monitoring, Inbox, Templates, Settings; no breadcrumb bar; the sidebar collapses to icons (rail / Ctrl+B).
- 768 and 375: the top bar (menu, page title, search); the sheet closes after picking a page; `/settings` is a list and sub-pages have "← Settings".
- ⌘K / Ctrl+K from a page and from inside a text field; Recent after a few jumps; on an app page Restart/Stop/Pull latest images/View logs.
- The user menu's Theme follows immediately and survives a reload; System follows the OS.
- Old addresses: `/org/alerts?tab=rules`, `/server/<id>/settings?tab=volumes`, `/user/tokens` land on the new pages.

Take screenshots at the three widths for the PR description. Stop the instance and delete its scratch directory.

- [ ] **Step 5: Commit any fixes**

```bash
git status --short
```

Expected: clean (or only the fixes, committed with `fix(web): …` and the `Co-Authored-By` line).

---

## Interfaces for later phases

Phases 2–7 build on these names; keep them stable.

**Routes** (`@/lib/routes`): `appsPath(serverId)`, `newAppPath(serverId, from?: "template" | "compose" | "git")`, `newAppSub(from?)`, `appPath(serverId, appId, tab?: AppTab)`, `appSub(appId, tab?)`, `monitoringPath(serverId)`, `serverSettingsPath(serverId, section?: "general" | "domains" | "storages" | "docker")`, `serverSettingsSub(section?)`, `settingsPath(section: "organization" | "members" | "variables" | "notifications" | "password" | "tokens")`, `templatePath(id)`, `INBOX_PATH`, `TEMPLATES_PATH`, `NEW_TEMPLATE_PATH`, `SETTINGS_PATH`, `type AppTab = "overview" | "logs" | "configuration" | "backups" | "metrics" | "settings"`, `LEGACY_APP_TABS` (phase 2 adds `history: "configuration"` and opens the history sheet when `?tab=history` arrives), `isWidePath(pathname)`. Route table: `appRoutes` in `web/src/routes.tsx`; `NewAppPage` (`/server/:id/new`, reads `?from=`) is the place phase 3 puts the chooser and forms.

**Page chrome**: `PageHeader({ title, subtitle?, back?: { to, label }, actions? })` (names the page too); `usePageTitle(title)` / `useCurrentPageTitle()` (`@/layouts/use-page-title`) for pages without a `PageHeader`.

**Primitives**: `StatusDot({ tone, pulse?, label? })`, `StatusPill({ status })` (`@/components/status`) with `appStatus(code)`, `serverStatus({ liveness, lastSeenAt, standalone })`, `backupStatus(state)`, `StatusTone`, `StatusInfo`, `TONE_DOT_CLASS`, `TONE_PILL_CLASS` (`@/lib/status`) and the Tailwind colours `status-ok|warn|bad|idle`; `ConfirmDialog(ConfirmDialogProps)`; `ResponsiveDialog(ResponsiveDialogProps)`, `PHONE_QUERY`; `EmptyState({ title, icon?, action? })`; `Section({ title, description?, actions?, children })`, `RowList({ label?, children })`, `Row({ title, leading?, description?, meta?, actions?, to? })`; `Disclosure({ label = "More options", defaultOpen?, open?, onOpenChange?, children })`; `UnderlineTabs<T>({ value, onValueChange, items, label?, children })` + `useTabParam<T>(tabs, fallback, { param?, aliases? })`; `useMediaQuery(query)`, `useIsMobile()`, `stubViewport(width)` for tests.

**Palette** (`@/context/use-palette`): `usePaletteActions(heading, actions: PaletteAction[])` — phase 2 registers the app page's actions (Restart, Stop/Start, Pull latest images, View logs, Back up now) from the new app page; `useCommandPalette().openPalette()`; `PaletteAction`, `PaletteActionGroup` (`@/context/palette-context-base`).

**Inbox** (`@/hooks/use-inbox-unread`): `useInboxUnread(): { count, seenAt, isUnread(createdAt), markAllRead() }` — phase 4's Inbox uses `isUnread` for the unread dot and `markAllRead` for "Mark all as read"; `unreadBadge(count)` and the storage helpers in `@/lib/inbox`.

**Settings** (`@/layouts/settings-layout`, `@/hooks/use-settings-sections`, `@/lib/settings-nav`): `SettingsLayout`, `SettingsIndex`, `useSettingsSections()`, `settingsSections({ server, hasPassword })` — phase 5 replaces the bodies of `ServerGeneralPage` / `ServerDomainsPage` / `ServerStoragesPage` / `ServerDockerPage` (`@/pages/server-settings`), `OrgMembersPage section=…`, `SharedVariablesSettingsPage`, `NotificationsSettingsPage`, `UserPasswordPage`, `UserTokensPage` without touching the routes.

**Theme**: `useThemePreference()` (`@/hooks/use-theme`), `ThemePreference`, `setThemePreference`, `THEME_KEY` (`@/lib/theme`).

**Keyboard**: `shortcutLabel(key)`, `modKeyLabel()`, `isPaletteShortcut(e)`, `serverShortcutIndex(e)` (`@/lib/keyboard`).
