# UX rebuild, phase 4: Monitoring and Inbox — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the Monitoring page and the Inbox around spec §5. Monitoring gets a `PageHeader`, a firing-alerts banner, four KPI tiles, four default charts, an "Apps" section, a collapsed "More details", and a collapsed "Alert rules for this server". The Inbox gets "Firing now" pinned on top, events grouped by day with plain titles, delivery notes and unread dots, All / Firing / Resolved filters, Load more, and an "All quiet" empty state.

**Architecture:** The wording moves into a pure module, `web/src/lib/alert-text.ts` (plain titles, target text, delivery notes, day groups), which is tested directly. The history paging that lived in `HistoryTab` becomes a hook, `useAlertEvents`. The hook also puts new events on top when an `alert` SSE notification arrives. Two alert components serve both the Monitoring page and the app Metrics tab. The first is a new `FiringBanner`. The second is the existing `TargetAlertsCard`, which keeps its props, is collapsed by default, and loads its rules only when opened. `HistoryTab` is deleted because the Inbox replaces it. No API change: the data comes from the existing `GET /api/v1/alerts/get-events` and `get-active` endpoints.

**Tech Stack:** React 19, React Router 7, TypeScript 5.9 (strict, `verbatimModuleSyntax`, `erasableSyntaxOnly`), Tailwind 4, vendored shadcn/ui (Radix Collapsible, ToggleGroup, Select), recharts, Vitest 5 + Testing Library 16 on jsdom. No new dependency, no Go change.

**Spec:** `docs/superpowers/specs/2026-10-13-ux-rebuild-design.md` §5 "Monitoring and Inbox". This is phase 4 of §8. It builds on phase 1 (`docs/superpowers/plans/2026-10-13-ux-rebuild-p1-shell.md`, its "Interfaces for later phases" section and Task 8 for the Inbox unread hook and the basic `/inbox` page). Phases 2–3 (Apps page, app page, new-app flows, Configuration) land before this one. They place `TargetAlertsCard` on the app Metrics tab. This plan changes only the component, never its call sites.

**Base:** branch `feat/ux-rebuild` after phases 1–3. All commands run from the worktree root. Before starting, read the current `web/src/pages/monitoring.tsx`, `web/src/pages/inbox.tsx`, `web/src/components/alerts/target-alerts-card.tsx` and their tests. Phases 1–3 edited them (page title, links). The files below are full replacements written against those edits. If a later phase added something that this plan does not mention, keep it.

## Global Constraints

- The spec is binding. This phase changes only the Monitoring page, the Inbox page, and the alert components they use. Frontend only: no Go change, no API change. `api/openapi.yaml` is untouched. No feature is removed: the per-target rule overrides, the history filters and the pagination all stay.
- Never delete anything under `web/src/components/ui/` (vendored shadcn/ui).
- Plain words: no env var names (e.g. `METRICS_DISK_INTERVAL`) in any UI string. Alert titles in the Inbox and the banners are plain sentences ("Website is down", "homelab CPU above 90%"), never the notification's emoji/operator form.
- Status colours come only from the theme tokens: `bg-status-bad`, `text-status-ok`, `border-status-bad/30`, `StatusDot`, `StatusPill`. Never use new hard-coded colours such as `text-emerald-600` or `bg-red-500`.
- Type scale: page title 24/600 via `PageHeader`; section title 15/600 via `Section`; body 14 (`text-sm`); spacing on the 4/8 grid. Monitoring is full width (`isWidePath`); the Inbox is capped at 1100px by the shell. Transitions are `duration-150` with `motion-reduce:` off.
- Mobile: charts stack in one column below `md` (768px). The KPI tiles stay two per row on phones (two compact tiles fit at 375px; one per row would push the charts a screen down) and four per row from `lg`. Inbox filters wrap. Tap targets are at least 40px (`min-h-10`/`h-10` on phones).
- Real router links (`<Link>`), no `window.confirm`, no `href="#"`.
- Lint (`eslint-plugin-react-hooks` 7): no `Date.now()` during render; use `useNow()`. No `ref.current` reads during render. `react-refresh`: component files export only components.
- TypeScript: `import type` for types, no `enum`, no unused locals/imports. When a step removes the last use of an import, remove the import.
- Tests: Vitest + Testing Library, files beside the code. Answer fetches with `fakeAPI` (`@/test/fake-api`) and SSE with `fakeNotifications` (`@/test/notifications`). Mock contexts with `vi.mock("@/context/use-servers", …)`. Keep the existing `data-testid`s: `chart-<title>`, `kpi-<label>`, `docker-df`, `data-as-of`, `event-row`, `target-rule`, `alerts-card` (the mock's).
- Coverage: changed web lines must be at least 80% covered (`tools/diffcov`). `*.test.tsx` and `web/src/components/ui/` are exempt.
- `pnpm --dir web run build` empties `web/dist`: run `git checkout -- web/dist/.gitkeep` after any build, before committing.
- Per task, before the commit: the task's Vitest files, `pnpm --dir web run lint` and `pnpm --dir web exec tsc -b` all pass.
- Commits: Conventional Commits (`feat(web): …`, `refactor(web): …`). Each message ends with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Alerts about nameless, renamed or deleted targets, and metrics this UI does not know** (an app with an empty `app_name`, a server named `""`, a metric a newer backend adds). Expected: every title still reads as a sentence ("An app is down"), never `undefined` or an empty subject; an unknown metric falls back to the notification's own title with the emoji stripped. Pinned in Task 1 (`alert-text.test.ts`, "names nameless targets", "falls back on an unknown metric").
2. **The Inbox open while alerts fire, with filters changed mid-request and older pages already loaded.** Expected: a new event appears on top once (no duplicate, loaded pages kept); a response to an outdated filter or an outdated first page never overwrites the newer list. Pinned in Task 2 (`use-alert-events.test.tsx`, "puts a new event on top…", "ignores a response to an outdated filter", "drops a live update made for an older filter").
3. **Day groups across midnight and with the server's clock ahead of the browser.** Expected: groups follow the browser's local day. An event stamped tomorrow is labelled with its date, not "Today". Two same-day events separated by another day's event form one group, never two groups with the same React key. Pinned in Task 1 (`dayLabel`, `groupByDay` tests).
4. **Filter combinations on the Inbox.** "Resolved" while alerts are firing, a server picked that is then removed (the select disappears below two servers), and a server filter applied to "Firing now". Expected: "Firing now" is hidden under Resolved and follows the server filter; a vanished server reads as "all servers" instead of silently filtering to nothing. Pinned in Task 4 (`inbox.test.tsx`, "filters…", "reads a removed server as all servers").
5. **A brand-new server on Monitoring** (no Docker disk scan yet, no temperature sensors, no per-app samples) **and the collapsed rules.** Expected: plain empty states without env names; no empty Temperature chart; the by-app charts say "No data yet"; the alert rules are fetched only when "Alert rules for this server" is opened (no idle cost). Pinned in Task 5 ("a fresh server…") and Task 3 ("loads nothing until opened").

## File map

| File | Change | Responsibility |
|---|---|---|
| `web/src/lib/alert-text.ts` (+ test) | create | plain alert titles, target text, delivery notes, day labels and groups |
| `web/src/hooks/use-alert-events.ts` (+ test) | create | paged alert history with filters, stale-response guard, live merge on SSE |
| `web/src/components/alerts/firing-banner.tsx` (+ test) | create | the firing-alerts banner for a server or an app |
| `web/src/components/alerts/target-alerts-card.tsx` (+ test) | rewrite | collapsed "Alert rules for this server/app" with per-target overrides; same props plus `defaultOpen?` |
| `web/src/components/alerts/inbox-rows.tsx` | create | `EventRow`, `FiringRow` (Inbox list rows) |
| `web/src/pages/inbox.tsx` (+ test) | rewrite | `/inbox` |
| `web/src/components/alerts/history-tab.tsx` (+ test) | delete | replaced by the Inbox |
| `web/src/components/data-freshness.tsx` | modify | root becomes an inline `<span>` so it can sit inside the `PageHeader` subtitle `<p>` |
| `web/src/pages/monitoring.tsx` (+ test) | rewrite | `/server/:id/monitoring` |

---

### Task 1: Plain alert wording

**Files:**
- Create: `web/src/lib/alert-text.ts`
- Test: `web/src/lib/alert-text.test.ts`

**Interfaces:**
- Consumes: `metricInfo`, `hasThreshold`, `formatAlertValue`, types `AlertEvent`, `ActiveAlert`, `Delivery` from `@/lib/alerts`.
- Produces (`@/lib/alert-text`):
  - `type AlertSubject = Pick<AlertEvent, "target_kind" | "metric" | "server_name" | "app_name" | "value" | "threshold">`
  - `alertTitle(kind: "fired" | "resolved", subject: AlertSubject, fallback?: string): string`
  - `eventTitle(ev: AlertEvent): string`, `activeTitle(a: ActiveAlert): string`
  - `targetText(t: Pick<AlertEvent, "target_kind" | "server_name" | "app_name">): string` returns `"Website on homelab"` or `"homelab"`
  - `type DeliveryNote = { text: string; failed: boolean; detail: string }`, `deliveryNote(deliveries: Delivery[] | null): DeliveryNote`
  - `dayLabel(createdAt: string, now: number): string` returns `"Today"`, `"Yesterday"` or a local date
  - `type DayGroup<T> = { key: string; label: string; items: T[] }`, `groupByDay<T extends { created_at: string }>(items: T[], now: number): DayGroup<T>[]`

- [ ] **Step 1: Write the failing test**

Create `web/src/lib/alert-text.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import {
  activeTitle,
  alertTitle,
  dayLabel,
  deliveryNote,
  eventTitle,
  groupByDay,
  targetText,
  type AlertSubject,
} from "@/lib/alert-text";
import type { ActiveAlert, AlertEvent, AlertMetric } from "@/lib/alerts";

const server: AlertSubject = { target_kind: "server", metric: "cpu", server_name: "homelab", app_name: "", value: 93.4, threshold: 90 };
const app: AlertSubject = { target_kind: "app", metric: "down", server_name: "homelab", app_name: "Website", value: 0, threshold: 0 };

const event = (over: Partial<AlertEvent> = {}): AlertEvent => ({
  id: "e1",
  rule_id: "r1",
  server_id: "s1",
  app_id: "",
  server_name: "homelab",
  app_name: "",
  target_kind: "server",
  metric: "cpu",
  kind: "fired",
  value: 93,
  threshold: 90,
  title: "🔥 homelab · CPU 93% > 90% for 10m",
  message: "",
  deliveries: [],
  created_at: "2026-10-13T10:00:00Z",
  ...over,
});

describe("alertTitle", () => {
  it("says what crossed which line, in plain words", () => {
    expect(alertTitle("fired", server)).toBe("homelab CPU above 90%");
    expect(alertTitle("resolved", { ...server, value: 41 })).toBe("homelab CPU back to 41%");
    expect(alertTitle("fired", { ...server, metric: "memory" })).toBe("homelab memory above 90%");
    expect(alertTitle("fired", { ...server, metric: "load1", threshold: 4 })).toBe("homelab load 1m above 4.00");
    expect(alertTitle("fired", { ...app, metric: "disk", threshold: 50 })).toBe("Website disk above 50.0 GB");
  });

  it("says down and back up for availability", () => {
    expect(alertTitle("fired", app)).toBe("Website is down");
    expect(alertTitle("resolved", app)).toBe("Website is back up");
    expect(alertTitle("fired", { ...server, metric: "down" })).toBe("homelab is down");
  });

  it("names nameless targets", () => {
    expect(alertTitle("fired", { ...app, app_name: "  " })).toBe("An app is down");
    expect(alertTitle("fired", { ...server, server_name: "" })).toBe("A server CPU above 90%");
  });

  it("falls back on an unknown metric", () => {
    const metric = "gpu" as AlertMetric;
    expect(alertTitle("fired", { ...server, metric }, "🔥 homelab · GPU 93% > 90% for 10m")).toBe(
      "homelab · GPU 93% > 90% for 10m",
    );
    expect(alertTitle("fired", { ...server, metric })).toBe("homelab: gpu alert");
  });

  it("titles history events and firing alerts", () => {
    expect(eventTitle(event({ kind: "resolved", value: 41 }))).toBe("homelab CPU back to 41%");
    const firing: ActiveAlert = { rule_id: "r1", since: "", server_id: "s1", app_id: "", ...server };
    expect(activeTitle(firing)).toBe("homelab CPU above 90%");
  });
});

describe("targetText", () => {
  it("is the server, or the app on its server", () => {
    expect(targetText(server)).toBe("homelab");
    expect(targetText(app)).toBe("Website on homelab");
    expect(targetText({ ...app, app_name: "", server_name: "" })).toBe("An app on a server");
  });
});

describe("deliveryNote", () => {
  it("says where the alert went", () => {
    expect(deliveryNote(null)).toEqual({ text: "Not sent", failed: false, detail: "" });
    expect(deliveryNote([])).toEqual({ text: "Not sent", failed: false, detail: "" });
    expect(deliveryNote([{ channel_id: "c1", channel_name: "Telegram", ok: true, error: "" }])).toEqual({
      text: "Sent to Telegram",
      failed: false,
      detail: "",
    });
  });

  it("names the channels it could not reach, with the reasons as detail", () => {
    expect(
      deliveryNote([
        { channel_id: "c1", channel_name: "Ops", ok: true, error: "" },
        { channel_id: "c2", channel_name: "Mail", ok: false, error: "535 auth failed" },
      ]),
    ).toEqual({ text: "Sent to Ops · Couldn't send to Mail", failed: true, detail: "Mail: 535 auth failed" });
  });
});

describe("day groups", () => {
  // Local times: the groups follow the browser's day, whatever its zone.
  const now = new Date(2026, 9, 13, 12, 0).getTime();
  const at = (day: number, hour: number) => new Date(2026, 9, day, hour, 0).toISOString();
  const longDate = (iso: string, year = false) =>
    new Date(iso).toLocaleDateString(undefined, {
      weekday: "long",
      month: "long",
      day: "numeric",
      ...(year ? { year: "numeric" } : {}),
    });

  it("labels today, yesterday, and older days by date", () => {
    expect(dayLabel(at(13, 0), now)).toBe("Today");
    expect(dayLabel(at(13, 23), now)).toBe("Today");
    expect(dayLabel(at(12, 23), now)).toBe("Yesterday");
    expect(dayLabel(at(12, 0), now)).toBe("Yesterday");
    expect(dayLabel(at(10, 9), now)).toBe(longDate(at(10, 9)));
    const lastYear = new Date(2025, 11, 31, 9).toISOString();
    expect(dayLabel(lastYear, now)).toBe(longDate(lastYear, true));
  });

  it("dates an event stamped ahead of this browser's day instead of calling it today", () => {
    expect(dayLabel(at(14, 1), now)).toBe(longDate(at(14, 1)));
  });

  it("reads a broken timestamp as an unknown date", () => {
    expect(dayLabel("soon", now)).toBe("Unknown date");
  });

  it("groups newest-first events by day, one group per day", () => {
    const items = [
      { id: "a", created_at: at(13, 11) },
      { id: "b", created_at: at(13, 9) },
      { id: "c", created_at: at(12, 22) },
      { id: "d", created_at: at(13, 8) }, // out of order: still today's group
    ];
    const groups = groupByDay(items, now);
    expect(groups.map((g) => g.label)).toEqual(["Today", "Yesterday"]);
    expect(groups[0].items.map((i) => i.id)).toEqual(["a", "b", "d"]);
    expect(new Set(groups.map((g) => g.key)).size).toBe(2);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm --dir web exec vitest run src/lib/alert-text.test.ts`
Expected: FAIL. The module `@/lib/alert-text` does not exist.

- [ ] **Step 3: Implement**

Create `web/src/lib/alert-text.ts`:

```ts
import {
  formatAlertValue,
  hasThreshold,
  metricInfo,
  type ActiveAlert,
  type AlertEvent,
  type Delivery,
} from "@/lib/alerts";

// Plain wording of alerts for the Inbox and the firing banners (spec §1
// "Plain words", §5): "Website is down", "homelab CPU above 90%". The
// notifications themselves keep their emoji and operators
// ("🔥 homelab · CPU 93% > 90% for 10m").

export type AlertSubject = Pick<
  AlertEvent,
  "target_kind" | "metric" | "server_name" | "app_name" | "value" | "threshold"
>;

type Target = Pick<AlertEvent, "target_kind" | "server_name" | "app_name">;

function serverName(t: Target): string {
  return t.server_name.trim() || "A server";
}

// subjectName is what an alert is about: the app for an app alert, else the
// server. A target without a name (not reconciled yet, or since removed) is
// still a subject.
function subjectName(t: Target): string {
  return t.target_kind === "app" ? t.app_name.trim() || "An app" : serverName(t);
}

// metricWord is a metric's label inside a sentence: "CPU" stays, the others
// go lower-case ("memory", "load 1m").
function metricWord(label: string): string {
  return label === "CPU" ? label : label.charAt(0).toLowerCase() + label.slice(1);
}

// stripMarks drops the emoji and punctuation a notification title starts
// with ("🔥 ", "✅ ").
function stripMarks(title: string): string {
  return title.replace(/^[^\p{L}\p{N}]+/u, "");
}

// alertTitle is one alert transition as a sentence. A metric this UI does
// not know (a newer backend) falls back to the notification's own title.
export function alertTitle(kind: "fired" | "resolved", subject: AlertSubject, fallback = ""): string {
  const name = subjectName(subject);
  const info = metricInfo(subject.target_kind, subject.metric);
  if (!info) return stripMarks(fallback) || `${name}: ${subject.metric} alert`;
  if (!hasThreshold(subject.metric)) return kind === "fired" ? `${name} is down` : `${name} is back up`;
  const word = metricWord(info.label);
  return kind === "fired"
    ? `${name} ${word} above ${formatAlertValue(info.unit, subject.threshold)}`
    : `${name} ${word} back to ${formatAlertValue(info.unit, subject.value)}`;
}

// eventTitle titles a history event.
export function eventTitle(ev: AlertEvent): string {
  return alertTitle(ev.kind, ev, ev.title);
}

// activeTitle titles an alert that is firing now.
export function activeTitle(a: ActiveAlert): string {
  return alertTitle("fired", a);
}

// targetText says where an alert is: "homelab", or "Website on homelab".
export function targetText(t: Target): string {
  if (t.target_kind !== "app") return serverName(t);
  return `${subjectName(t)} on ${t.server_name.trim() || "a server"}`;
}

export type DeliveryNote = {
  text: string;
  // failed is true when at least one channel could not be reached.
  failed: boolean;
  // detail is one "channel: error" line per failed channel (a tooltip).
  detail: string;
};

// deliveryNote is the small line saying where an event's notification went:
// "Not sent", "Sent to Telegram", "Sent to Ops · Couldn't send to Mail".
export function deliveryNote(deliveries: Delivery[] | null): DeliveryNote {
  const list = deliveries ?? [];
  if (list.length === 0) return { text: "Not sent", failed: false, detail: "" };
  const sent = list.filter((d) => d.ok).map((d) => d.channel_name);
  const failed = list.filter((d) => !d.ok);
  const parts: string[] = [];
  if (sent.length > 0) parts.push(`Sent to ${sent.join(", ")}`);
  if (failed.length > 0) parts.push(`Couldn't send to ${failed.map((d) => d.channel_name).join(", ")}`);
  return {
    text: parts.join(" · "),
    failed: failed.length > 0,
    detail: failed.map((d) => `${d.channel_name}: ${d.error}`).join("\n"),
  };
}

function startOfDay(ms: number): number {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

// dayLabel names an event's local day: "Today", "Yesterday", else its date
// ("Saturday, October 10", with the year when it is not this year's). A day
// after today (the server's clock ahead of this browser's) gets its date.
export function dayLabel(createdAt: string, now: number): string {
  const day = startOfDay(Date.parse(createdAt));
  if (!Number.isFinite(day)) return "Unknown date";
  const today = startOfDay(now);
  if (day === today) return "Today";
  // today - 1 ms is the last moment of yesterday, also across a DST change.
  if (day === startOfDay(today - 1)) return "Yesterday";
  const date = new Date(day);
  const sameYear = date.getFullYear() === new Date(now).getFullYear();
  return date.toLocaleDateString(undefined, {
    weekday: "long",
    month: "long",
    day: "numeric",
    ...(sameYear ? {} : { year: "numeric" }),
  });
}

export type DayGroup<T> = { key: string; label: string; items: T[] };

// groupByDay splits newest-first items into one group per local day, in the
// order the days first appear. Every item of a day lands in that day's single
// group, even when the list is not perfectly ordered.
export function groupByDay<T extends { created_at: string }>(items: T[], now: number): DayGroup<T>[] {
  const groups = new Map<number, DayGroup<T>>();
  for (const item of items) {
    const day = startOfDay(Date.parse(item.created_at));
    let group = groups.get(day);
    if (!group) {
      group = { key: String(day), label: dayLabel(item.created_at, now), items: [] };
      groups.set(day, group);
    }
    group.items.push(item);
  }
  return [...groups.values()];
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `pnpm --dir web exec vitest run src/lib/alert-text.test.ts`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/alert-text.ts web/src/lib/alert-text.test.ts
git commit -m "$(cat <<'EOF'
feat(web): plain alert titles, delivery notes and day groups

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `useAlertEvents`, the paged history with live updates

**Files:**
- Create: `web/src/hooks/use-alert-events.ts`
- Test: `web/src/hooks/use-alert-events.test.tsx`

**Interfaces:**
- Consumes: `alertsGet` (`@/hooks/use-alerts`), `ALERT_NOTIFICATION`, `AlertEvent` (`@/lib/alerts`), `NotificationsContext` (`@/context/notifications-context-base`).
- Produces (`@/hooks/use-alert-events`):
  - `type EventKindFilter = "all" | "fired" | "resolved"`
  - `type AlertEventsState = { events: AlertEvent[]; hasMore: boolean; loading: boolean; error: string | null; loadMore: () => void }`
  - `useAlertEvents(kind: EventKindFilter, serverId: string | null): AlertEventsState` reads `get-events` with `kind`, `server_id` and `cursor` (in that query order). A filter change starts over. Responses to older requests are dropped. On an `alert` notification it reads the first page again and puts the unseen events on top, keeping the loaded pages and the cursor.

- [ ] **Step 1: Write the failing test**

Create `web/src/hooks/use-alert-events.test.tsx`:

```tsx
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useAlertEvents, type EventKindFilter } from "@/hooks/use-alert-events";
import type { AlertEvent } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";
import { fakeNotifications } from "@/test/notifications";

afterEach(() => vi.unstubAllGlobals());

const event = (id: string, over: Partial<AlertEvent> = {}): AlertEvent => ({
  id,
  rule_id: "r1",
  server_id: "s1",
  app_id: "",
  server_name: "prod-1",
  app_name: "",
  target_kind: "server",
  metric: "cpu",
  kind: "fired",
  value: 93,
  threshold: 90,
  title: `title ${id}`,
  message: "",
  deliveries: [],
  created_at: "2026-10-11T12:00:00Z",
  ...over,
});

type Props = { kind: EventKindFilter; server: string | null };

describe("useAlertEvents", () => {
  it("reads the newest page with the filters and pages on", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-events": (_body: unknown, q: URLSearchParams) =>
        q.get("cursor") ? { events: [event("e3")], next_cursor: "" } : { events: [event("e1"), event("e2")], next_cursor: "c2" },
    });
    const { result, rerender } = renderHook(({ kind, server }: Props) => useAlertEvents(kind, server), {
      initialProps: { kind: "all", server: null },
    });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["e1", "e2"]));
    expect(result.current.loading).toBe(false);
    expect(result.current.hasMore).toBe(true);
    expect(api.calls[0].query).toBe("");

    act(() => result.current.loadMore());
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["e1", "e2", "e3"]));
    expect(api.calls.at(-1)?.query).toBe("?cursor=c2");
    expect(result.current.hasMore).toBe(false);

    rerender({ kind: "fired", server: "s2" });
    await waitFor(() => expect(api.calls.at(-1)?.query).toBe("?kind=fired&server_id=s2"));
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["e1", "e2"]));
  });

  it("says why the history could not be read", async () => {
    fakeAPI({ "/api/v1/alerts/get-events": new Error("database is locked") });
    const { result } = renderHook(() => useAlertEvents("all", null));
    await waitFor(() => expect(result.current.error).toBe("database is locked"));
    expect(result.current.loading).toBe(false);
    expect(result.current.events).toEqual([]);
  });

  it("ignores a response to an outdated filter", async () => {
    const pending: { query: string; resolve: (page: unknown) => void }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        (url: string) =>
          new Promise<Response>((resolve) => {
            pending.push({
              query: new URL(url, "http://x").search,
              resolve: (page) => resolve(new Response(JSON.stringify({ success: true, message: "ok", data: page }))),
            });
          }),
      ),
    );
    const { result, rerender } = renderHook(({ kind, server }: Props) => useAlertEvents(kind, server), {
      initialProps: { kind: "all", server: null },
    });
    await waitFor(() => expect(pending).toHaveLength(1));
    rerender({ kind: "fired", server: null });
    rerender({ kind: "resolved", server: null });
    await waitFor(() => expect(pending).toHaveLength(3));
    expect(pending[1].query).toBe("?kind=fired");
    expect(pending[2].query).toBe("?kind=resolved");

    pending[2].resolve({ events: [event("newer", { kind: "resolved" })], next_cursor: "" });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["newer"]));
    pending[1].resolve({ events: [event("stale")], next_cursor: "" });
    pending[0].resolve({ events: [event("oldest")], next_cursor: "" });
    await act(() => new Promise((r) => setTimeout(r, 50)));
    expect(result.current.events.map((e) => e.id)).toEqual(["newer"]);
    expect(result.current.loading).toBe(false);
  });

  it("puts a new event on top when an alert fires, keeping the loaded pages", async () => {
    let first = [event("e1")];
    fakeAPI({
      "/api/v1/alerts/get-events": (_body: unknown, q: URLSearchParams) =>
        q.get("cursor") ? { events: [event("old")], next_cursor: "" } : { events: first, next_cursor: "c1" },
    });
    const sse = fakeNotifications();
    const { result } = renderHook(() => useAlertEvents("all", null), { wrapper: sse.Provider });
    const ids = () => result.current.events.map((e) => e.id);
    await waitFor(() => expect(ids()).toEqual(["e1"]));
    act(() => result.current.loadMore());
    await waitFor(() => expect(ids()).toEqual(["e1", "old"]));

    first = [event("e2"), event("e1")];
    act(() => sse.emit({ type: "alert", ref: "", payload: {} }));
    await waitFor(() => expect(ids()).toEqual(["e2", "e1", "old"]));
    // The cursor of the loaded pages is untouched: nothing more to load.
    expect(result.current.hasMore).toBe(false);

    act(() => sse.emit({ type: "apps_status", ref: "", payload: {} }));
    act(() => sse.emit({ type: "alert", ref: "", payload: {} }));
    await new Promise((r) => setTimeout(r, 50));
    expect(ids()).toEqual(["e2", "e1", "old"]);
  });

  it("drops a live update made for an older filter", async () => {
    const pending: { query: string; resolve: (page: unknown) => void }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        (url: string) =>
          new Promise<Response>((resolve) => {
            pending.push({
              query: new URL(url, "http://x").search,
              resolve: (page) => resolve(new Response(JSON.stringify({ success: true, message: "ok", data: page }))),
            });
          }),
      ),
    );
    const sse = fakeNotifications();
    const { result, rerender } = renderHook(({ kind, server }: Props) => useAlertEvents(kind, server), {
      initialProps: { kind: "all", server: null },
      wrapper: sse.Provider,
    });
    await waitFor(() => expect(pending).toHaveLength(1));
    pending[0].resolve({ events: [event("e1")], next_cursor: "" });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["e1"]));
    act(() => sse.emit({ type: "alert", ref: "", payload: {} })); // live read for "all"
    await waitFor(() => expect(pending).toHaveLength(2));
    rerender({ kind: "resolved", server: null });
    await waitFor(() => expect(pending).toHaveLength(3));
    pending[2].resolve({ events: [event("r1", { kind: "resolved" })], next_cursor: "" });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["r1"]));
    pending[1].resolve({ events: [event("f9")], next_cursor: "" });
    await act(() => new Promise((r) => setTimeout(r, 50)));
    expect(result.current.events.map((e) => e.id)).toEqual(["r1"]);
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm --dir web exec vitest run src/hooks/use-alert-events.test.tsx`
Expected: FAIL. The module `@/hooks/use-alert-events` does not exist.

- [ ] **Step 3: Implement**

Create `web/src/hooks/use-alert-events.ts`:

```ts
import { useCallback, useContext, useEffect, useRef, useState } from "react";

import { NotificationsContext } from "@/context/notifications-context-base";
import { alertsGet } from "@/hooks/use-alerts";
import { ALERT_NOTIFICATION, type AlertEvent } from "@/lib/alerts";

type Page = { events: AlertEvent[] | null; next_cursor: string };

// EventKindFilter is the Inbox's All / Firing / Resolved filter ("fired" is
// the API's kind of a firing transition).
export type EventKindFilter = "all" | "fired" | "resolved";

export type AlertEventsState = {
  events: AlertEvent[];
  hasMore: boolean;
  loading: boolean;
  error: string | null;
  loadMore: () => void;
};

function eventParams(kind: EventKindFilter, serverId: string | null, cursor: string): Record<string, string> {
  const params: Record<string, string> = {};
  if (kind !== "all") params.kind = kind;
  if (serverId) params.server_id = serverId;
  if (cursor) params.cursor = cursor;
  return params;
}

// useAlertEvents is the org's alert history, newest first, filtered by kind
// and server and paged by the API's cursor. A filter change starts over. A
// response to an older request (filters changed meanwhile) is dropped
// instead of overwriting the newer list. While mounted, every "alert"
// notification reads the first page again and puts the events not shown
// yet on top. The loaded pages and the cursor stay, and there is no polling.
export function useAlertEvents(kind: EventKindFilter, serverId: string | null): AlertEventsState {
  const [events, setEvents] = useState<AlertEvent[]>([]);
  const [cursor, setCursor] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // latest numbers page requests: only the newest may land.
  const latest = useRef(0);
  // generation numbers first-page loads: a live update read before the
  // filters changed is dropped.
  const generation = useRef(0);

  const load = useCallback(
    async (after: string) => {
      const request = ++latest.current;
      if (!after) generation.current++;
      setLoading(true);
      try {
        const page = await alertsGet<Page>("get-events", eventParams(kind, serverId, after));
        if (request !== latest.current) return;
        const got = page.events ?? [];
        setEvents((prev) => (after ? [...prev, ...got] : got));
        setCursor(page.next_cursor);
        setError(null);
      } catch (e) {
        if (request !== latest.current) return;
        setError(e instanceof Error ? e.message : "Failed to load alerts");
      } finally {
        if (request === latest.current) setLoading(false);
      }
    },
    [kind, serverId],
  );

  useEffect(() => {
    void load("");
  }, [load]);

  const mergeNewest = useCallback(async () => {
    const gen = generation.current;
    try {
      const page = await alertsGet<Page>("get-events", eventParams(kind, serverId, ""));
      if (gen !== generation.current) return;
      setEvents((prev) => {
        const known = new Set(prev.map((e) => e.id));
        const fresh = (page.events ?? []).filter((e) => !known.has(e.id));
        return fresh.length > 0 ? [...fresh, ...prev] : prev;
      });
    } catch {
      // A live update is best effort: the list stays as it is.
    }
  }, [kind, serverId]);

  const subscribe = useContext(NotificationsContext)?.subscribe;
  useEffect(() => {
    if (!subscribe) return;
    return subscribe((n) => {
      if (n.type === ALERT_NOTIFICATION) void mergeNewest();
    });
  }, [subscribe, mergeNewest]);

  const loadMore = useCallback(() => {
    if (cursor) void load(cursor);
  }, [cursor, load]);

  return { events, hasMore: cursor !== "", loading, error, loadMore };
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `pnpm --dir web exec vitest run src/hooks/use-alert-events.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/hooks/use-alert-events.ts web/src/hooks/use-alert-events.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): paged alert history hook with live updates on new alerts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Firing banner, and the alert rules as a collapsed section

**Files:**
- Create: `web/src/components/alerts/firing-banner.tsx`, `web/src/components/alerts/firing-banner.test.tsx`
- Modify (rewrite): `web/src/components/alerts/target-alerts-card.tsx`, `web/src/components/alerts/target-alerts-card.test.tsx`

**Interfaces:**
- Consumes: `activeTitle` (Task 1); `useActiveAlerts`, `alertsGet` (`@/hooks/use-alerts`); `useNow`; `alertTargetPath`, `sinceText`, `conditionText`, `effective`, `hasThreshold`, `metricInfo`, types `Channel`, `Override`, `Rule`, `TargetKind` (`@/lib/alerts`); `INBOX_PATH`, `settingsPath` (`@/lib/routes`); `StatusDot`, `StatusPill` (`@/components/status`); `type StatusInfo` (`@/lib/status`); `Disclosure` (`@/components/disclosure`); `OverrideDialog` (unchanged).
- Produces:
  - `FiringBanner({ serverId: string; appId?: string })` (`@/components/alerts/firing-banner`). It renders nothing while quiet. Otherwise it is a region "Firing alerts": "N alert(s) firing", one plain line per alert with "for 12 min", and "Inbox →". On a server it also lists the server's apps' alerts, linking each to the app's Metrics tab. With `appId` it shows only that app's alerts.
  - `TargetAlertsCard({ serverId: string; appId?: string; targetLabel: string; defaultOpen?: boolean })` (same export and props as before, plus `defaultOpen`). It is a region "Alert rules": a `Disclosure` labelled "Alert rules for this server" or "Alert rules for this app", collapsed by default, with "Manage rules" linking to `settingsPath("notifications")`. Opened, it loads and lists every org rule of that kind with its effective setting for this target, a "Firing" pill on rules firing here now, "Adjusted"/"Off here" badges, and the Adjust (override) dialog. Nothing is fetched while it is collapsed.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/alerts/firing-banner.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { FiringBanner } from "@/components/alerts/firing-banner";
import type { ActiveAlert } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const since = new Date(Date.now() - 12 * 60_000).toISOString();
const serverCPU: ActiveAlert = {
  rule_id: "r1", target_kind: "server", metric: "cpu", server_id: "s1", app_id: "",
  since, value: 93.4, threshold: 90, server_name: "prod-1", app_name: "",
};
const appDown: ActiveAlert = {
  rule_id: "r4", target_kind: "app", metric: "down", server_id: "s1", app_id: "a1",
  since, value: 0, threshold: 0, server_name: "prod-1", app_name: "nextcloud",
};

describe("FiringBanner", () => {
  it("lists the server's firing alerts and its apps', in plain words", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": [serverCPU, appDown] });
    render(
      <MemoryRouter>
        <FiringBanner serverId="s1" />
      </MemoryRouter>,
    );
    const banner = await screen.findByRole("region", { name: "Firing alerts" });
    expect(banner).toHaveTextContent("2 alerts firing");
    expect(banner).toHaveTextContent("prod-1 CPU above 90%");
    expect(banner).toHaveTextContent("for 12 min");
    expect(within(banner).getByRole("link", { name: "nextcloud is down" })).toHaveAttribute(
      "href",
      "/server/s1/app/a1?tab=metrics",
    );
    // The server's own alert is about the page it is on: no link.
    expect(within(banner).queryByRole("link", { name: "prod-1 CPU above 90%" })).not.toBeInTheDocument();
    expect(within(banner).getByRole("link", { name: /Inbox/ })).toHaveAttribute("href", "/inbox");
    expect(api.calls[0].query).toBe("?server_id=s1");
  });

  it("keeps to one app's alerts on an app", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": [appDown] });
    render(
      <MemoryRouter>
        <FiringBanner serverId="s1" appId="a1" />
      </MemoryRouter>,
    );
    const banner = await screen.findByRole("region", { name: "Firing alerts" });
    expect(banner).toHaveTextContent("1 alert firing");
    expect(banner).toHaveTextContent("nextcloud is down");
    expect(within(banner).queryByRole("link", { name: "nextcloud is down" })).not.toBeInTheDocument();
    expect(api.calls[0].query).toBe("?server_id=s1&app_id=a1");
  });

  it("renders nothing while all is quiet", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": [] });
    const { container } = render(
      <MemoryRouter>
        <FiringBanner serverId="s1" />
      </MemoryRouter>,
    );
    await vi.waitFor(() => expect(api.calls).toHaveLength(1));
    expect(container).toBeEmptyDOMElement();
  });
});
```

Replace the whole of `web/src/components/alerts/target-alerts-card.test.tsx` with:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { TargetAlertsCard } from "@/components/alerts/target-alerts-card";
import type { Override, Rule } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const base = { channel_ids: null, enabled: true, created_at: "2026-10-11T12:00:00Z" };
const rules: Rule[] = [
  { ...base, id: "r1", target_kind: "server", metric: "cpu", threshold: 90, duration_minutes: 10 },
  { ...base, id: "r2", target_kind: "server", metric: "disk", threshold: 90, duration_minutes: 5 },
  { ...base, id: "r3", target_kind: "server", metric: "down", threshold: 0, duration_minutes: 5 },
  { ...base, id: "r4", target_kind: "app", metric: "down", threshold: 0, duration_minutes: 5 },
];
const overrides: Override[] = [
  { rule_id: "r1", server_id: "s1", app_id: "", disabled: false, threshold: 95, duration_minutes: null, channel_ids: null },
  { rule_id: "r2", server_id: "s1", app_id: "", disabled: true, threshold: null, duration_minutes: null, channel_ids: null },
];
const now = () => new Date().toISOString();

function renderCard(props: { appId?: string; targetLabel?: string } = {}) {
  return render(
    <MemoryRouter>
      <TargetAlertsCard serverId="s1" targetLabel={props.targetLabel ?? "prod-1"} appId={props.appId} />
    </MemoryRouter>,
  );
}

describe("TargetAlertsCard", () => {
  it("is a collapsed section that loads nothing until opened", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/get-overrides": [],
      "/api/v1/alerts/get-channels": [],
      "/api/v1/alerts/get-active": [],
    });
    renderCard();
    const region = screen.getByRole("region", { name: "Alert rules" });
    const toggle = within(region).getByRole("button", { name: "Alert rules for this server" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(within(region).getByRole("link", { name: "Manage rules" })).toHaveAttribute("href", "/settings/notifications");
    await new Promise((r) => setTimeout(r, 20));
    expect(api.calls).toHaveLength(0);

    await userEvent.setup({ delay: null }).click(toggle);
    expect(await screen.findAllByTestId("target-rule")).toHaveLength(3);
    expect(api.calls.map((c) => c.path)).toContain("/api/v1/alerts/get-rules");
  });

  it("shows every server rule with its effective setting and marks the ones firing here", async () => {
    fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/get-overrides": overrides,
      "/api/v1/alerts/get-channels": [],
      "/api/v1/alerts/get-active": [
        { rule_id: "r1", target_kind: "server", metric: "cpu", server_id: "s1", app_id: "", since: now(), value: 97, threshold: 95, server_name: "prod-1", app_name: "" },
        { rule_id: "r4", target_kind: "app", metric: "down", server_id: "s1", app_id: "a1", since: now(), value: 0, threshold: 0, server_name: "prod-1", app_name: "x" },
      ],
    });
    renderCard();
    await userEvent.setup({ delay: null }).click(screen.getByRole("button", { name: "Alert rules for this server" }));

    const rows = await screen.findAllByTestId("target-rule");
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent("CPU above 95% for 10 min");
    expect(within(rows[0]).getByText("Adjusted")).toBeInTheDocument();
    expect(await within(rows[0]).findByText("Firing")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Off here")).toBeInTheDocument();
    expect(within(rows[1]).queryByText("Firing")).not.toBeInTheDocument();
    expect(rows[2]).toHaveTextContent("Down for 5 min");
    // The app's alert belongs to the app's section, not the server's.
    expect(within(rows[2]).queryByText("Firing")).not.toBeInTheDocument();
  });

  it("is about the app on an app, and can start open", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/get-overrides": [],
      "/api/v1/alerts/get-channels": [],
      "/api/v1/alerts/get-active": [],
    });
    render(
      <MemoryRouter>
        <TargetAlertsCard serverId="s1" appId="a1" targetLabel="nextcloud" defaultOpen />
      </MemoryRouter>,
    );
    expect(screen.getByRole("button", { name: "Alert rules for this app" })).toHaveAttribute("aria-expanded", "true");
    const rows = await screen.findAllByTestId("target-rule");
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveTextContent("Down for 5 min");
    expect(api.calls.find((c) => c.path === "/api/v1/alerts/get-overrides")?.query).toBe("?server_id=s1&app_id=a1");
  });

  it("says when the organization has no rules for this kind of target", async () => {
    fakeAPI({
      "/api/v1/alerts/get-rules": rules.filter((r) => r.target_kind === "app"),
      "/api/v1/alerts/get-overrides": [],
      "/api/v1/alerts/get-channels": [],
      "/api/v1/alerts/get-active": [],
    });
    render(
      <MemoryRouter>
        <TargetAlertsCard serverId="s1" targetLabel="prod-1" defaultOpen />
      </MemoryRouter>,
    );
    expect(await screen.findByText("No server rules yet.")).toBeInTheDocument();
  });

  it("saves and resets an override for this target", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/get-overrides": overrides,
      "/api/v1/alerts/get-channels": [{ id: "c1", name: "Ops", enabled: true }],
      "/api/v1/alerts/get-active": [],
      "/api/v1/alerts/save-override": {},
      "/api/v1/alerts/delete-override": null,
    });
    const user = userEvent.setup({ delay: null });
    renderCard();
    await user.click(screen.getByRole("button", { name: "Alert rules for this server" }));

    await user.click(await screen.findByRole("button", { name: "Adjust Down for prod-1" }));
    expect(screen.queryByLabelText(/Above/)).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("For (minutes)"), "15");
    await user.click(screen.getByLabelText("Specific channels"));
    await user.click(screen.getByLabelText("Ops"));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.posted("/api/v1/alerts/save-override")).toEqual([
      { rule_id: "r3", server_id: "s1", app_id: "", disabled: false, threshold: null, duration_minutes: 15, channel_ids: ["c1"] },
    ]);

    await user.click(screen.getByRole("button", { name: "Adjust CPU for prod-1" }));
    expect(screen.getByLabelText(/Above/)).toHaveValue(95);
    await user.click(screen.getByRole("button", { name: "Reset to rule" }));
    expect(api.posted("/api/v1/alerts/delete-override")).toEqual([{ rule_id: "r1", server_id: "s1", app_id: "" }]);
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/alerts/firing-banner.test.tsx src/components/alerts/target-alerts-card.test.tsx`
Expected: FAIL. `firing-banner` does not exist; the card has no "Alert rules for this server" toggle.

- [ ] **Step 3: Implement**

Create `web/src/components/alerts/firing-banner.tsx`:

```tsx
import { Link } from "react-router-dom";
import { ArrowRight, TriangleAlert } from "lucide-react";

import { StatusDot } from "@/components/status";
import { useActiveAlerts } from "@/hooks/use-alerts";
import { useNow } from "@/hooks/use-now";
import { alertTargetPath, sinceText } from "@/lib/alerts";
import { activeTitle } from "@/lib/alert-text";
import { INBOX_PATH } from "@/lib/routes";

// FiringBanner is the top banner of a server's Monitoring page (or an
// app's page): what is firing there now, in plain words, with how long.
// On a server it also lists its apps' alerts and links each one to the app.
// It renders nothing while all is quiet.
export function FiringBanner({ serverId, appId }: { serverId: string; appId?: string }) {
  const { data } = useActiveAlerts(serverId, appId || null);
  const now = useNow();
  const firing = appId ? data.filter((a) => a.app_id === appId) : data;
  if (firing.length === 0) return null;
  return (
    <div
      role="region"
      aria-label="Firing alerts"
      className="rounded-xl border border-status-bad/30 bg-status-bad/5 px-4 py-3"
    >
      <div className="flex items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-status-bad">
          <TriangleAlert className="size-4" aria-hidden />
          {firing.length} alert{firing.length === 1 ? "" : "s"} firing
        </h2>
        <Link
          to={INBOX_PATH}
          className="inline-flex min-h-8 items-center gap-1 text-xs text-muted-foreground transition-colors duration-150 hover:text-foreground"
        >
          Inbox
          <ArrowRight className="size-3.5" aria-hidden />
        </Link>
      </div>
      <ul className="mt-1 grid gap-1 text-sm">
        {firing.map((a) => {
          const title = activeTitle(a);
          // An app's alert on its server's page leads to the app; an alert
          // about the page itself needs no link.
          const linked = Boolean(a.app_id) && !appId;
          return (
            <li key={`${a.rule_id}-${a.app_id}`} className="flex min-w-0 items-center gap-2">
              <StatusDot tone="bad" />
              {linked ? (
                <Link to={alertTargetPath(a.server_id, a.app_id)} className="truncate font-medium hover:underline">
                  {title}
                </Link>
              ) : (
                <span className="truncate font-medium">{title}</span>
              )}
              <span className="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums">
                for {sinceText(a.since, now)}
              </span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
```

Replace the whole of `web/src/components/alerts/target-alerts-card.tsx` with:

```tsx
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { SlidersHorizontal } from "lucide-react";

import { OverrideDialog } from "@/components/alerts/override-dialog";
import { Disclosure } from "@/components/disclosure";
import { StatusPill } from "@/components/status";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { alertsGet, useActiveAlerts } from "@/hooks/use-alerts";
import {
  conditionText,
  effective,
  hasThreshold,
  metricInfo,
  type Channel,
  type Override,
  type Rule,
  type TargetKind,
} from "@/lib/alerts";
import { settingsPath } from "@/lib/routes";
import type { StatusInfo } from "@/lib/status";
import { cn } from "@/lib/utils";

type Props = {
  serverId: string;
  // appId targets one app; empty or absent targets the server itself.
  appId?: string;
  // targetLabel names the target in dialogs ("prod-1", "nextcloud").
  targetLabel: string;
  // defaultOpen starts the section expanded (collapsed by default).
  defaultOpen?: boolean;
};

const FIRING: StatusInfo = { tone: "bad", label: "Firing" };

// TargetAlertsCard is "Alert rules for this server/app", collapsed under a
// server's Monitoring charts and an app's Metrics tab (spec §5). Opened, it
// lists every org rule of the target's kind with its effective setting here,
// adjustable per target (overrides). The rules are read only once it is
// opened: a collapsed section costs nothing.
export function TargetAlertsCard({ serverId, appId = "", targetLabel, defaultOpen = false }: Props) {
  const kind: TargetKind = appId ? "app" : "server";
  return (
    <section aria-label="Alert rules" className="relative">
      <Link
        to={settingsPath("notifications")}
        className="absolute top-0 right-0 inline-flex min-h-10 items-center text-xs text-muted-foreground transition-colors duration-150 hover:text-foreground hover:underline"
      >
        Manage rules
      </Link>
      <Disclosure label={`Alert rules for this ${kind}`} defaultOpen={defaultOpen}>
        <TargetRules serverId={serverId} appId={appId} kind={kind} targetLabel={targetLabel} />
      </Disclosure>
    </section>
  );
}

// TargetRules is the opened section's body; it mounts (and fetches) only
// while the Disclosure is open.
function TargetRules({
  serverId,
  appId,
  kind,
  targetLabel,
}: {
  serverId: string;
  appId: string;
  kind: TargetKind;
  targetLabel: string;
}) {
  const { data: active } = useActiveAlerts(serverId, appId || null);
  const firing = new Set(active.filter((a) => a.app_id === appId).map((a) => a.rule_id));
  const [rules, setRules] = useState<Rule[]>([]);
  const [overrides, setOverrides] = useState<Override[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [adjusting, setAdjusting] = useState<Rule | null>(null);
  // version bumps after an override changes, reloading the rules.
  const [version, setVersion] = useState(0);

  useEffect(() => {
    let current = true;
    const target: Record<string, string> = { server_id: serverId };
    if (appId) target.app_id = appId;
    Promise.all([
      alertsGet<Rule[] | null>("get-rules"),
      alertsGet<Override[] | null>("get-overrides", target),
      alertsGet<Channel[] | null>("get-channels"),
    ])
      .then(([r, o, c]) => {
        if (!current) return;
        setRules((r ?? []).filter((x) => x.target_kind === kind));
        setOverrides(o ?? []);
        setChannels(c ?? []);
        setError(null);
        setLoaded(true);
      })
      .catch((e: unknown) => {
        if (current) setError(e instanceof Error ? e.message : "Failed to load alert rules");
      });
    return () => {
      current = false;
    };
  }, [serverId, appId, kind, version]);

  return (
    <div className="space-y-2">
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      <ul className="grid gap-1.5" aria-label="Rules">
        {rules.map((r) => {
          const o = overrides.find((x) => x.rule_id === r.id);
          const eff = effective(r, o);
          const info = metricInfo(r.target_kind, r.metric);
          return (
            <li key={r.id} className="flex min-h-10 items-center gap-2 text-sm" data-testid="target-rule">
              <span className={cn("min-w-0 flex-1 truncate", !eff.enabled && "text-muted-foreground line-through")}>
                {hasThreshold(r.metric) ? (
                  <>
                    <span className="font-medium">{info?.label ?? r.metric}</span>{" "}
                    {conditionText(r.metric, info?.unit ?? "", eff.threshold, eff.duration_minutes)}
                  </>
                ) : (
                  <span className="font-medium">Down for {eff.duration_minutes} min</span>
                )}
              </span>
              {firing.has(r.id) ? <StatusPill status={FIRING} /> : null}
              {o ? <Badge variant="secondary">{o.disabled ? "Off here" : "Adjusted"}</Badge> : null}
              <Button
                variant="ghost"
                size="icon"
                className="size-8"
                aria-label={`Adjust ${info?.label ?? r.metric} for ${targetLabel}`}
                onClick={() => setAdjusting(r)}
              >
                <SlidersHorizontal className="size-3.5" />
              </Button>
            </li>
          );
        })}
        {loaded && rules.length === 0 ? (
          <li className="text-sm text-muted-foreground">No {kind} rules yet.</li>
        ) : null}
      </ul>

      {adjusting ? (
        <OverrideDialog
          key={adjusting.id}
          open
          onOpenChange={(open) => !open && setAdjusting(null)}
          rule={adjusting}
          override={overrides.find((x) => x.rule_id === adjusting.id)}
          channels={channels}
          serverId={serverId}
          appId={appId}
          targetLabel={targetLabel}
          onSaved={() => setVersion((v) => v + 1)}
        />
      ) : null}
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/alerts/firing-banner.test.tsx src/components/alerts/target-alerts-card.test.tsx src/components/app-metrics.test.tsx`
Expected: PASS. `app-metrics.test.tsx` mocks the card, and its call site keeps compiling because the props did not change.

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/alerts/firing-banner.tsx web/src/components/alerts/firing-banner.test.tsx web/src/components/alerts/target-alerts-card.tsx web/src/components/alerts/target-alerts-card.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): firing banner, and alert rules as a collapsed section

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: The Inbox

**Files:**
- Create: `web/src/components/alerts/inbox-rows.tsx`
- Modify (rewrite): `web/src/pages/inbox.tsx`, `web/src/pages/inbox.test.tsx`
- Delete: `web/src/components/alerts/history-tab.tsx`, `web/src/components/alerts/history-tab.test.tsx`

**Interfaces:**
- Consumes: `eventTitle`, `activeTitle`, `targetText`, `deliveryNote`, `groupByDay` (Task 1); `useAlertEvents`, `EventKindFilter` (Task 2); `useInboxUnread` (phase 1: `{ count, isUnread(createdAt), markAllRead() }`); `useActiveAlerts`; `useServers`; `useNow`; `alertTargetPath`, `sinceText` (`@/lib/alerts`); `formatClock` (`@/lib/kpi`); `PageHeader`, `Section`, `RowList`, `EmptyState`, `StatusDot`; `settingsPath`.
- Produces:
  - `EventRow({ event: AlertEvent; unread: boolean })` and `FiringRow({ alert: ActiveAlert; now: number })` (`@/components/alerts/inbox-rows`). Each is an `<li>` whose title link covers the row and opens the target (an app's Metrics tab or the server's Monitoring). The `data-testid` values are `"event-row"` and `"firing-row"`.
  - default `InboxPage` (`@/pages/inbox`), same route as phase 1.

- [ ] **Step 1: Write the failing test**

Replace the whole of `web/src/pages/inbox.test.tsx` with:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { dayLabel } from "@/lib/alert-text";
import type { ActiveAlert, AlertEvent } from "@/lib/alerts";
import InboxPage from "@/pages/inbox";
import { fakeAPI } from "@/test/fake-api";

// Local times: "today" is Oct 13 at noon in whatever zone the runner uses.
const NOW = new Date(2026, 9, 13, 12, 0);
const at = (day: number, hour: number, minute = 0) => new Date(2026, 9, day, hour, minute).toISOString();
// The user last looked at 11:00 today.
const SEEN = new Date(2026, 9, 13, 11, 0).getTime();

const markAllRead = vi.fn();
let unread = 2;
vi.mock("@/hooks/use-inbox-unread", () => ({
  useInboxUnread: () => ({
    count: unread,
    seenAt: SEEN,
    isUnread: (createdAt: string) => Date.parse(createdAt) > SEEN,
    markAllRead,
  }),
}));
let servers = [
  { id: "s1", name: "homelab" },
  { id: "s2", name: "lab" },
];
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers }) }));

const event = (id: string, over: Partial<AlertEvent> = {}): AlertEvent => ({
  id,
  rule_id: "r1",
  server_id: "s1",
  app_id: "",
  server_name: "homelab",
  app_name: "",
  target_kind: "server",
  metric: "cpu",
  kind: "fired",
  value: 93,
  threshold: 90,
  title: "",
  message: "",
  deliveries: [],
  created_at: at(13, 10),
  ...over,
});

const ops = { channel_id: "c1", channel_name: "Ops", ok: true, error: "" };
const mail = { channel_id: "c2", channel_name: "Mail", ok: false, error: "535 auth failed" };
const history: AlertEvent[] = [
  event("e1", { app_id: "a1", app_name: "Website", target_kind: "app", metric: "down", threshold: 0, created_at: at(13, 11, 30), deliveries: [ops] }),
  event("e2", { kind: "resolved", value: 41, created_at: at(13, 9) }),
  event("e3", { created_at: at(12, 22), deliveries: [ops, mail] }),
  event("e4", { kind: "resolved", value: 40, created_at: at(10, 9) }),
];
const firingCPU: ActiveAlert = {
  rule_id: "r9", target_kind: "server", metric: "cpu", server_id: "s1", app_id: "",
  since: at(13, 11, 48), value: 95, threshold: 90, server_name: "homelab", app_name: "",
};

function renderPage(
  {
    events = history,
    active = [firingCPU],
    next = "",
  }: { events?: AlertEvent[]; active?: ActiveAlert[]; next?: string } = {},
) {
  const api = fakeAPI({
    "/api/v1/alerts/get-events": (_b: unknown, q: URLSearchParams) =>
      q.get("cursor") ? { events: [event("e9", { created_at: at(9, 9) })], next_cursor: "" } : { events, next_cursor: next },
    "/api/v1/alerts/get-active": active,
  });
  const view = render(
    <MemoryRouter>
      <InboxPage />
    </MemoryRouter>,
  );
  return { api, ...view };
}

const lastEventsQuery = (api: ReturnType<typeof fakeAPI>) =>
  api.calls.filter((c) => c.path === "/api/v1/alerts/get-events").at(-1)?.query;

describe("InboxPage", () => {
  beforeEach(() => {
    vi.useFakeTimers({ now: NOW, toFake: ["Date"] });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    markAllRead.mockClear();
    unread = 2;
    servers = [
      { id: "s1", name: "homelab" },
      { id: "s2", name: "lab" },
    ];
  });

  it("is org-wide with a way to the notification settings and Mark all as read", async () => {
    renderPage();
    expect(screen.getByRole("heading", { level: 1, name: "Inbox" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Notification settings/ })).toHaveAttribute("href", "/settings/notifications");
    await userEvent.setup().click(screen.getByRole("button", { name: "Mark all as read" }));
    expect(markAllRead).toHaveBeenCalled();
  });

  it("offers Mark all as read only when something is unread", () => {
    unread = 0;
    renderPage();
    expect(screen.queryByRole("button", { name: "Mark all as read" })).not.toBeInTheDocument();
  });

  it("pins what is firing now above the history", async () => {
    renderPage();
    const firing = await screen.findByRole("region", { name: "Firing now" });
    const row = within(firing).getByTestId("firing-row");
    expect(within(row).getByRole("link", { name: "homelab CPU above 90%" })).toHaveAttribute("href", "/server/s1/monitoring");
    expect(row).toHaveTextContent("for 12 min");
    const today = await screen.findByRole("region", { name: "Today" });
    expect(firing.compareDocumentPosition(today) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("groups the history by day in plain words, with delivery notes and unread dots", async () => {
    renderPage();
    const today = await screen.findByRole("region", { name: "Today" });
    const rows = within(today).getAllByTestId("event-row");
    expect(rows).toHaveLength(2);

    expect(within(rows[0]).getByRole("link", { name: "Website is down" })).toHaveAttribute("href", "/server/s1/app/a1?tab=metrics");
    expect(rows[0]).toHaveTextContent("Website on homelab");
    expect(rows[0]).toHaveTextContent("Sent to Ops");
    expect(within(rows[0]).getByRole("img", { name: "Fired" })).toBeInTheDocument();
    expect(within(rows[0]).getByRole("img", { name: "Unread" })).toBeInTheDocument();

    expect(within(rows[1]).getByRole("link", { name: "homelab CPU back to 41%" })).toHaveAttribute("href", "/server/s1/monitoring");
    expect(within(rows[1]).getByLabelText("Resolved")).toBeInTheDocument();
    expect(rows[1]).toHaveTextContent("Not sent");
    expect(within(rows[1]).queryByRole("img", { name: "Unread" })).not.toBeInTheDocument();

    const yesterday = screen.getByRole("region", { name: "Yesterday" });
    const failed = within(yesterday).getByText("Sent to Ops · Couldn't send to Mail");
    expect(failed).toHaveAttribute("title", "Mail: 535 auth failed");
    expect(screen.getByRole("region", { name: dayLabel(at(10, 9), NOW.getTime()) })).toBeInTheDocument();
    // No notification emoji or operators anywhere.
    expect(document.body).not.toHaveTextContent(/🔥|✅|>/);
  });

  it("filters by All / Firing / Resolved and by server", async () => {
    const { api } = renderPage();
    await screen.findByRole("region", { name: "Firing now" });
    const user = userEvent.setup();

    await user.click(screen.getByRole("radio", { name: "Firing" }));
    expect(lastEventsQuery(api)).toBe("?kind=fired");
    expect(screen.getByRole("region", { name: "Firing now" })).toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: "Resolved" }));
    expect(lastEventsQuery(api)).toBe("?kind=resolved");
    expect(screen.queryByRole("region", { name: "Firing now" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("combobox", { name: "Server" }));
    await user.click(await screen.findByRole("option", { name: "lab" }));
    expect(lastEventsQuery(api)).toBe("?kind=resolved&server_id=s2");
    expect(api.calls.filter((c) => c.path === "/api/v1/alerts/get-active").at(-1)?.query).toBe("?server_id=s2");

    await user.click(screen.getByRole("radio", { name: "All" }));
    expect(lastEventsQuery(api)).toBe("?server_id=s2");
  });

  it("offers the server filter only with two servers or more", async () => {
    servers = [{ id: "s1", name: "homelab" }];
    renderPage();
    await screen.findByRole("region", { name: "Today" });
    expect(screen.queryByRole("combobox", { name: "Server" })).not.toBeInTheDocument();
  });

  it("reads a removed server as all servers", async () => {
    const { api, rerender } = renderPage();
    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox", { name: "Server" }));
    await user.click(await screen.findByRole("option", { name: "lab" }));
    expect(lastEventsQuery(api)).toBe("?server_id=s2");

    servers = [{ id: "s1", name: "homelab" }];
    rerender(
      <MemoryRouter>
        <InboxPage />
      </MemoryRouter>,
    );
    await vi.waitFor(() => expect(lastEventsQuery(api)).toBe(""));
  });

  it("loads more of the history", async () => {
    const { api } = renderPage({ next: "c1" });
    await screen.findByRole("region", { name: "Today" });
    await userEvent.setup().click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByRole("region", { name: dayLabel(at(9, 9), NOW.getTime()) })).toBeInTheDocument();
    expect(lastEventsQuery(api)).toBe("?cursor=c1");
    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();
  });

  it("says all is quiet when there is nothing", async () => {
    renderPage({ events: [], active: [] });
    expect(
      await screen.findByText("All quiet. You'll see alerts here when something needs attention."),
    ).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm --dir web exec vitest run src/pages/inbox.test.tsx`
Expected: FAIL. The phase-1 page still renders `HistoryTab` (no "Firing now", no day regions, no radios).

- [ ] **Step 3: Implement**

Create `web/src/components/alerts/inbox-rows.tsx`:

```tsx
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { CircleCheck } from "lucide-react";

import { StatusDot } from "@/components/status";
import { alertTargetPath, sinceText, type ActiveAlert, type AlertEvent } from "@/lib/alerts";
import { activeTitle, deliveryNote, eventTitle, targetText } from "@/lib/alert-text";
import { formatClock } from "@/lib/kpi";
import { cn } from "@/lib/utils";

// Flat rows with a hover wash; on a phone each row is its own card (the
// RowList around them drops its frame there).
const ROW =
  "relative flex items-start gap-3 px-4 py-3 transition-colors duration-150 hover:bg-muted/40 motion-reduce:transition-none max-sm:rounded-xl max-sm:border max-sm:bg-card";
// The title link covers the whole row: a click anywhere opens the target.
const COVER = "block truncate text-sm after:absolute after:inset-0 focus-visible:underline focus-visible:outline-none";

function Mark({ children }: { children?: ReactNode }) {
  return <span className="flex size-5 shrink-0 items-center justify-center">{children}</span>;
}

// EventRow is one history event: unread dot, red dot (fired) or green check
// (resolved), the plain title, then target · time · where it was sent.
export function EventRow({ event, unread }: { event: AlertEvent; unread: boolean }) {
  const note = deliveryNote(event.deliveries);
  return (
    <li className={ROW} data-testid="event-row">
      <span className="flex h-5 w-2 shrink-0 items-center">
        {unread ? <span role="img" aria-label="Unread" className="size-2 rounded-full bg-primary" /> : null}
      </span>
      <Mark>
        {event.kind === "fired" ? (
          <StatusDot tone="bad" label="Fired" />
        ) : (
          <CircleCheck className="size-4 text-status-ok" aria-label="Resolved" />
        )}
      </Mark>
      <div className="min-w-0 flex-1 space-y-0.5">
        <Link
          to={alertTargetPath(event.server_id, event.app_id)}
          className={cn(COVER, unread ? "font-semibold" : "font-medium")}
        >
          {eventTitle(event)}
        </Link>
        <div className="flex flex-wrap items-center gap-x-1.5 text-xs text-muted-foreground">
          <span className="truncate">{targetText(event)}</span>
          <span aria-hidden>·</span>
          <time dateTime={event.created_at} className="tabular-nums">
            {formatClock(Date.parse(event.created_at))}
          </time>
          <span aria-hidden>·</span>
          {/* Above the row's link so the failure reason shows on hover. */}
          <span
            className={cn("relative z-10", note.failed && "text-status-bad")}
            title={note.detail || undefined}
          >
            {note.text}
          </span>
        </div>
      </div>
    </li>
  );
}

// FiringRow is an alert firing now: red dot, plain title, target and how
// long it has been firing.
export function FiringRow({ alert, now }: { alert: ActiveAlert; now: number }) {
  return (
    <li className={ROW} data-testid="firing-row">
      <span className="w-2 shrink-0" aria-hidden />
      <Mark>
        <StatusDot tone="bad" label="Firing" />
      </Mark>
      <div className="min-w-0 flex-1 space-y-0.5">
        <Link to={alertTargetPath(alert.server_id, alert.app_id)} className={cn(COVER, "font-semibold")}>
          {activeTitle(alert)}
        </Link>
        <div className="flex flex-wrap items-center gap-x-1.5 text-xs text-muted-foreground">
          <span className="truncate">{targetText(alert)}</span>
          <span aria-hidden>·</span>
          <span className="tabular-nums">for {sinceText(alert.since, now)}</span>
        </div>
      </div>
    </li>
  );
}
```

Replace the whole of `web/src/pages/inbox.tsx` with:

```tsx
import { useState } from "react";
import { Link } from "react-router-dom";
import { ArrowRight, Inbox as InboxIcon } from "lucide-react";

import { EventRow, FiringRow } from "@/components/alerts/inbox-rows";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { RowList, Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useServers } from "@/context/use-servers";
import { useAlertEvents, type EventKindFilter } from "@/hooks/use-alert-events";
import { useActiveAlerts } from "@/hooks/use-alerts";
import { useInboxUnread } from "@/hooks/use-inbox-unread";
import { useNow } from "@/hooks/use-now";
import { groupByDay } from "@/lib/alert-text";
import { settingsPath } from "@/lib/routes";

const ALL_SERVERS = "all";
const SHOW: { value: EventKindFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "fired", label: "Firing" },
  { value: "resolved", label: "Resolved" },
];
const QUIET = "All quiet. You'll see alerts here when something needs attention.";
const FILTER_ITEM =
  "h-full min-w-0 rounded-md px-3 text-xs font-medium text-muted-foreground hover:bg-transparent hover:text-foreground data-[state=on]:bg-background data-[state=on]:text-foreground data-[state=on]:shadow-sm";

// InboxPage is /inbox, org-wide (spec §5): what is firing now pinned on top,
// then the alert history grouped by day, newest first, with unread dots
// (newer than the user's last "Mark all as read", kept in this browser).
// Filters: All / Firing / Resolved, and a server filter when there are
// several servers.
export default function InboxPage() {
  const { servers } = useServers();
  const { count, isUnread, markAllRead } = useInboxUnread();
  const [show, setShow] = useState<EventKindFilter>("all");
  const [picked, setPicked] = useState(ALL_SERVERS);
  // The server filter exists with two servers or more; a picked server that
  // went away (or the filter disappearing) reads as all servers.
  const serverFilter = servers.length > 1;
  const serverId = serverFilter && servers.some((s) => s.id === picked) ? picked : null;
  const { events, hasMore, loading, error, loadMore } = useAlertEvents(show, serverId);
  const { data: active } = useActiveAlerts(serverId);
  const now = useNow();
  const firing = show === "resolved" ? [] : active;
  const groups = groupByDay(events, now);
  const quiet = !loading && !error && events.length === 0 && firing.length === 0;

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

      <div className="flex flex-wrap items-center gap-2">
        <ToggleGroup
          type="single"
          size="sm"
          value={show}
          // Clicking the selected filter would unset it; the value stays.
          onValueChange={(v) => v && setShow(v as EventKindFilter)}
          aria-label="Show"
          className="h-10 gap-0 rounded-lg bg-muted p-[3px] sm:h-9"
        >
          {SHOW.map((o) => (
            <ToggleGroupItem key={o.value} value={o.value} className={FILTER_ITEM}>
              {o.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        {serverFilter ? (
          <Select value={serverId ?? ALL_SERVERS} onValueChange={setPicked}>
            <SelectTrigger className="h-10 w-44 sm:h-9" aria-label="Server">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_SERVERS}>All servers</SelectItem>
              {servers.map((s) => (
                <SelectItem key={s.id} value={s.id}>
                  {s.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
      </div>

      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      {firing.length > 0 ? (
        <Section title="Firing now">
          <RowList label="Firing now">
            {firing.map((a) => (
              <FiringRow key={`${a.rule_id}-${a.server_id}-${a.app_id}`} alert={a} now={now} />
            ))}
          </RowList>
        </Section>
      ) : null}

      {quiet ? <EmptyState icon={InboxIcon} title={QUIET} /> : null}

      {groups.map((g) => (
        <Section key={g.key} title={g.label}>
          <RowList label={g.label}>
            {g.items.map((ev) => (
              <EventRow key={ev.id} event={ev} unread={isUnread(ev.created_at)} />
            ))}
          </RowList>
        </Section>
      ))}

      {hasMore ? (
        <Button variant="outline" size="sm" onClick={loadMore} disabled={loading}>
          Load more
        </Button>
      ) : null}
    </div>
  );
}
```

Delete the history tab (the Inbox replaces it; nothing else imports it):

```bash
git rm web/src/components/alerts/history-tab.tsx web/src/components/alerts/history-tab.test.tsx
grep -rn "history-tab\|HistoryTab" web/src --include=*.ts --include=*.tsx | grep -v "pages/app-details"
```

Expected: no output. `app-details.tsx` may still have a local function named `HistoryTab` (the app's version history), which is unrelated.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/pages/inbox.test.tsx src/hooks/use-inbox-unread.test.tsx src/routes.test.tsx`
Expected: PASS. `routes.test.tsx` (phase 1) still finds the Inbox at `/inbox` and `/org/alerts`. If it asserted text that the old page rendered (e.g. "No alerts yet"), change that one expectation to the heading `Inbox`.

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/alerts/inbox-rows.tsx web/src/pages/inbox.tsx web/src/pages/inbox.test.tsx
# (the history-tab deletions were staged by git rm in Step 3)
git commit -m "$(cat <<'EOF'
feat(web): Inbox with firing-now, day groups, plain titles and filters

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: The Monitoring page

**Files:**
- Modify: `web/src/components/data-freshness.tsx`
- Modify (rewrite): `web/src/pages/monitoring.tsx`, `web/src/pages/monitoring.test.tsx`

**Interfaces:**
- Consumes: `FiringBanner`, `TargetAlertsCard` (Task 3); `PageHeader`, `Section`, `Disclosure`, `EmptyState` (phase 1); `KpiTile`, `KpiTileSkeleton`, `MetricChartCard`, `ChartCardSkeleton`, `ChartEmpty`, `RangeMenu`, `DataFreshness`; `useServerMetrics`, `useMetricsOverview`; the `@/lib/kpi` and `@/lib/metrics` helpers the page already uses.
- Produces: default `MonitoringPage` (same route). Layout top to bottom:
  - `PageHeader`: title "Monitoring"; the subtitle is the server name · `DataFreshness`; the actions slot holds `RangeMenu`.
  - `FiringBanner`.
  - KPI tiles: CPU, Memory, Disk, Network (`data-testid="kpi-grid"`).
  - Charts CPU, Memory, Disk usage, Network (`data-testid="host-charts"`, one column below `md`).
  - Section "Apps" with CPU by app and Memory by app.
  - `Disclosure` "More details" with Load, Disk I/O, Temperature (only with sensors) and Docker disk usage.
  - `TargetAlertsCard` (collapsed).
- `DataFreshness` keeps its props; its root becomes `<span class="inline-flex …">`.

- [ ] **Step 1: Write the failing test**

Replace the whole of `web/src/pages/monitoring.test.tsx` with:

```tsx
import { cloneElement, type ReactElement } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import MonitoringPage from "@/pages/monitoring";
import type { DockerDF, ServerSample, ServerSeries } from "@/lib/metrics";

const useServerMetrics = vi.fn();
const refreshServer = vi.fn(async () => {});
const refreshOverview = vi.fn(async () => {});
let data: ServerSeries | null = null;
let loading = false;
let latestDF: DockerDF | undefined;
let latestServer: Partial<ServerSample> | undefined;

// jsdom has no layout: a fixed-size ResponsiveContainer makes recharts draw.
vi.mock("recharts", async (importOriginal) => {
  const mod = await importOriginal<typeof import("recharts")>();
  return {
    ...mod,
    ResponsiveContainer: ({ children }: { children: ReactElement<{ width?: number; height?: number }> }) => (
      <div style={{ width: 600, height: 180 }}>{cloneElement(children, { width: 600, height: 180 })}</div>
    ),
  };
});

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServer: { id: "s1", name: "prod-1" }, activeServerId: "s1" }),
}));
vi.mock("@/context/use-apps", () => ({
  useApps: () => ({ apps: [{ id: "app-1", name: "Nextcloud" }, { id: "app-2-unnamed", name: "" }] }),
}));
// The alert components have their own tests; here they only have to be placed.
vi.mock("@/components/alerts/target-alerts-card", () => ({
  TargetAlertsCard: (p: { serverId: string; appId?: string }) => (
    <div data-testid="alerts-card">{`${p.serverId}/${p.appId ?? ""}`}</div>
  ),
}));
vi.mock("@/components/alerts/firing-banner", () => ({
  FiringBanner: (p: { serverId: string; appId?: string }) => <div data-testid="firing-banner">{p.serverId}</div>,
}));
vi.mock("@/hooks/use-metrics", () => ({
  useServerMetrics: (...args: unknown[]) => {
    useServerMetrics(...args);
    return { data, loading, error: null, updatedAt: data ? Date.parse("2026-10-11T17:06:00") : null, refresh: refreshServer };
  },
  useMetricsOverview: () => ({
    data: {
      servers: [
        { server_id: "other", latest: { at: "", server: { docker_df: { images: 1, volumes: 1, build_cache: 1, reclaimable: 1 } } } },
        {
          server_id: "s1",
          latest: latestDF || latestServer ? { at: "", server: { ...latestServer, docker_df: latestDF } } : null,
        },
      ],
    },
    loading: false,
    error: null,
    updatedAt: null,
    refresh: refreshOverview,
  }),
}));

function fills(title: string): (string | null)[] {
  return [...screen.getByTestId(`chart-${title}`).querySelectorAll("path.recharts-area-area")].map((p) =>
    p.getAttribute("fill"),
  );
}

function renderPage() {
  return render(
    <MemoryRouter>
      <MonitoringPage />
    </MemoryRouter>,
  );
}

const openDetails = () => userEvent.click(screen.getByRole("button", { name: "More details" }));

// Rendering every chart through recharts is slow on a busy CI box.
describe("MonitoringPage", { timeout: 20_000 }, () => {
  beforeEach(() => {
    useServerMetrics.mockClear();
    refreshServer.mockClear();
    refreshOverview.mockClear();
    loading = false;
    latestDF = undefined;
    latestServer = undefined;
    data = {
      interval_seconds: 60,
      points: [
        { t: 0, cpu: 5, mem_used: 1, mem_total: 4, load1: 1, "temp:Composite": 40 },
        {
          t: 60,
          cpu: 6,
          mem_used: 2,
          mem_total: 4,
          load1: 1,
          "temp:Composite": 41,
          docker_images: 1024 ** 3,
          docker_volumes: 0,
          docker_build_cache: 0,
          docker_reclaimable: 512,
        },
      ],
      apps: [{ app_id: "app-1", points: [{ t: 0, cpu: 1, mem_used: 5 }] }],
    };
  });

  it("heads the page with the server, freshness and the range, then the firing banner", () => {
    renderPage();
    const header = screen.getByRole("banner");
    expect(within(header).getByRole("heading", { level: 1, name: "Monitoring" })).toBeInTheDocument();
    expect(header).toHaveTextContent("prod-1");
    // Freshness sits in the subtitle paragraph: phrasing content only.
    expect(screen.getByTestId("data-as-of").closest("p")).not.toBeNull();
    expect(within(header).getByRole("button", { name: "Range: Last hour" })).toBeInTheDocument();
    const banner = screen.getByTestId("firing-banner");
    expect(banner).toHaveTextContent("s1");
    expect(banner.compareDocumentPosition(screen.getByTestId("kpi-CPU")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("shows four tiles, the four host charts and the apps, with the rest behind More details", async () => {
    renderPage();
    expect(useServerMetrics).toHaveBeenLastCalledWith("s1", "1h");
    for (const label of ["CPU", "Memory", "Disk", "Network"]) expect(screen.getByTestId(`kpi-${label}`)).toBeInTheDocument();
    const host = screen.getByTestId("host-charts");
    for (const title of ["CPU", "Memory", "Disk usage", "Network"]) {
      expect(within(host).getByTestId(`chart-${title}`)).toBeInTheDocument();
    }
    const apps = screen.getByRole("region", { name: "Apps" });
    for (const title of ["CPU by app", "Memory by app"]) expect(within(apps).getByTestId(`chart-${title}`)).toBeInTheDocument();
    for (const title of ["Load", "Disk I/O", "Temperature"]) expect(screen.queryByTestId(`chart-${title}`)).not.toBeInTheDocument();
    expect(screen.queryByTestId("docker-df")).not.toBeInTheDocument();
    // Rates were never reported: that chart says so instead of drawing zeros.
    expect(screen.getByTestId("chart-Network")).toHaveTextContent("No data yet");
    expect(screen.getByTestId("alerts-card")).toHaveTextContent("s1/");

    await openDetails();
    for (const title of ["Load", "Disk I/O", "Temperature"]) expect(screen.getByTestId(`chart-${title}`)).toBeInTheDocument();
    expect(screen.getByTestId("docker-df")).toHaveTextContent("Images1.0 GB");
    expect(screen.getByTestId("docker-df")).toHaveTextContent("Reclaimable512 B");
  });

  it("stacks the charts in one column on a phone", () => {
    renderPage();
    for (const grid of [screen.getByTestId("host-charts"), within(screen.getByRole("region", { name: "Apps" })).getByTestId("app-charts")]) {
      expect(grid).toHaveClass("grid", "md:grid-cols-2");
      expect(grid).not.toHaveClass("grid-cols-2");
    }
  });

  it("colors temperature series through CSS-safe keys", async () => {
    data!.points = [
      { t: 0, "temp:Core 0": 40, "temp:nvme Composite": 30 },
      { t: 60, "temp:Core 0": 41, "temp:nvme Composite": 31 },
    ];
    renderPage();
    await openDetails();
    const css = screen.getByTestId("chart-Temperature").querySelector("style")?.textContent ?? "";
    expect(css).toContain("--color-temp0:");
    expect(css).toContain("--color-temp1:");
    for (const m of css.matchAll(/(--color-[^;]*?):\s*var\(/g)) expect(m[1]).toMatch(/^--color-[A-Za-z0-9_-]+$/);
    expect(css).not.toContain("temp:");
  });

  it("explains a fresh server in plain words: no sensors, no disk scan yet, no app samples", async () => {
    data = { interval_seconds: 60, points: [{ t: 0, cpu: 1 }], apps: [] };
    renderPage();
    expect(screen.getByTestId("chart-CPU by app")).toHaveTextContent("No data yet");
    await openDetails();
    expect(screen.queryByTestId("chart-Temperature")).not.toBeInTheDocument();
    expect(screen.getByText("No disk scan yet. It runs once an hour.")).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent(/[A-Z]{3,}_[A-Z]{3,}/);
  });

  it("shows the latest Docker disk scan whatever the range", async () => {
    data!.points = [{ t: 0, cpu: 1 }]; // a coarse range without the scan
    latestDF = { images: 2 * 1024 ** 3, volumes: 0, build_cache: 0, reclaimable: 0 };
    renderPage();
    await openDetails();
    expect(screen.getByTestId("docker-df")).toHaveTextContent("Images2.0 GB");
  });

  it("draws temperatures and load as lines", async () => {
    data!.points = [
      { t: 0, load1: 1, load5: 1, load15: 1, "temp:a": 40, "temp:b": 30 },
      { t: 60, load1: 2, load5: 1, load15: 1, "temp:a": 41, "temp:b": 31 },
    ];
    renderPage();
    await openDetails();
    for (const title of ["Temperature", "Load"]) {
      expect(fills(title).length).toBeGreaterThan(0);
      for (const f of fills(title)) expect(f).toBe("none");
    }
  });

  it("stacks per-app averages but draws per-app peaks as lines", async () => {
    data!.apps = [
      { app_id: "app-1", points: [{ t: 0, cpu: 1, cpu_max: 3 }, { t: 60, cpu: 2, cpu_max: 4 }] },
      { app_id: "app-2-unnamed", points: [{ t: 0, cpu: 1, cpu_max: 3 }, { t: 60, cpu: 2, cpu_max: 4 }] },
    ];
    renderPage();
    for (const f of fills("CPU by app")) expect(f).not.toBe("none");
    expect(screen.getByTestId("chart-CPU by app")).toHaveTextContent("Nextcloud");
    expect(screen.getByTestId("chart-CPU by app")).toHaveTextContent("app-2-un"); // nameless: id prefix
    await userEvent.click(screen.getByRole("radio", { name: "Peak" }));
    expect(fills("CPU by app").length).toBe(2);
    for (const f of fills("CPU by app")) expect(f).toBe("none");
  });

  it("refetches for a new range", async () => {
    renderPage();
    await userEvent.click(screen.getByRole("button", { name: "Range: Last hour" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: "Last 7 days" }));
    expect(useServerMetrics).toHaveBeenLastCalledWith("s1", "7d");
  });

  it("leads with KPI tiles: the newest value and the trend within the range", () => {
    // An hour of minutes: the older half at the low values, the newer half
    // at the high ones.
    data!.points = Array.from({ length: 60 }, (_, i) =>
      i < 30
        ? { t: i * 60, cpu: 10, mem_used: 1, mem_total: 4, net_rx_bps: 100, net_tx_bps: 100 }
        : { t: i * 60, cpu: 20, mem_used: 2, mem_total: 4, net_rx_bps: 1024, net_tx_bps: 1024 },
    );
    renderPage();
    const cpu = screen.getByTestId("kpi-CPU");
    expect(cpu).toHaveTextContent("20%");
    // Usage rising is bad news: a red pill.
    expect(cpu.querySelector("[data-testid=kpi-delta]")).toHaveTextContent("+100%vs previous 30 min");
    expect(cpu.querySelector("[data-tone]")).toHaveAttribute("data-tone", "bad");
    expect(screen.getByTestId("kpi-Memory")).toHaveTextContent("50%");
    expect(screen.getByTestId("kpi-Memory")).toHaveTextContent("2 B of 4 B");
    expect(screen.getByTestId("kpi-Network")).toHaveTextContent("2.0 KB/s");
    expect(screen.getByTestId("kpi-Network")).toHaveTextContent("In 1.0 KB/s · Out 1.0 KB/s");
    expect(screen.getByTestId("kpi-Network").querySelector("[data-tone]")).toHaveAttribute("data-tone", "neutral");
    // No disk series at all: no figure and no pill rather than a wild one.
    expect(screen.getByTestId("kpi-Disk")).toHaveTextContent("–");
    expect(screen.getByTestId("kpi-Disk").querySelector("[data-testid=kpi-delta]")).toBeNull();
  });

  it("hides the trend when the range is mostly empty", () => {
    // A server that came up five minutes ago, viewed on the last hour.
    data!.points = Array.from({ length: 5 }, (_, i) => ({ t: i * 60, cpu: 10 + i }));
    renderPage();
    expect(screen.getByTestId("kpi-CPU").querySelector("[data-testid=kpi-delta]")).toBeNull();
  });

  it("takes headline figures from the live sample, not the range's last bucket", () => {
    latestServer = {
      cpu: 42,
      mem_used: 3,
      mem_total: 4,
      load1: 0.5,
      disks: [{ mount: "/", used: 1, total: 4 }],
      net_rx_bps: 2048,
      net_tx_bps: 0,
    };
    renderPage();
    expect(screen.getByTestId("kpi-CPU")).toHaveTextContent("42%");
    expect(screen.getByTestId("kpi-CPU")).toHaveTextContent("Load 0.50");
    expect(screen.getByTestId("kpi-Memory")).toHaveTextContent("75%");
    expect(screen.getByTestId("kpi-Memory")).toHaveTextContent("3 B of 4 B");
    expect(screen.getByTestId("kpi-Disk")).toHaveTextContent("25%");
    expect(screen.getByTestId("kpi-Network")).toHaveTextContent("2.0 KB/s");
  });

  it("states freshness and re-fetches on refresh", async () => {
    renderPage();
    expect(screen.getByTestId("data-as-of")).toHaveTextContent(/^Data as of /);
    await userEvent.click(screen.getByRole("button", { name: "Refresh data" }));
    expect(refreshServer).toHaveBeenCalledTimes(1);
    expect(refreshOverview).toHaveBeenCalledTimes(1);
  });

  it("keeps focus on the refresh button and ignores presses while refreshing", async () => {
    let finish: () => void = () => {};
    refreshServer.mockImplementationOnce(() => new Promise<void>((r) => (finish = r)));
    renderPage();
    const button = screen.getByRole("button", { name: "Refresh data" });
    button.focus();
    await userEvent.keyboard("{Enter}");
    expect(button).toHaveFocus();
    expect(button).toHaveAttribute("aria-busy", "true");
    await userEvent.keyboard("{Enter}");
    expect(refreshServer).toHaveBeenCalledTimes(1);
    finish();
    await waitFor(() => expect(button).toHaveAttribute("aria-busy", "false"));
  });

  it("shows skeletons shaped like the tiles and charts while loading", () => {
    data = null;
    loading = true;
    renderPage();
    // The CPU tile and the CPU chart.
    expect(screen.getAllByRole("status", { name: "Loading CPU" })).toHaveLength(2);
    expect(screen.getAllByRole("status").length).toBeGreaterThan(4);
    expect(screen.queryByTestId("chart-CPU")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "More details" })).not.toBeInTheDocument();
    expect(screen.getByTestId("data-as-of")).toHaveTextContent("Loading data");
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm --dir web exec vitest run src/pages/monitoring.test.tsx`
Expected: FAIL. There is no "Monitoring" heading, no `firing-banner`, no "Apps" region and no "More details".

- [ ] **Step 3: Implement**

In `web/src/components/data-freshness.tsx`, make the root an inline span so it is valid inside a paragraph (the `PageHeader` subtitle). Replace

```tsx
    <div className={cn("flex items-center gap-1 text-xs text-muted-foreground tabular-nums", className)}>
```

with

```tsx
    <span className={cn("inline-flex items-center gap-1 text-xs text-muted-foreground tabular-nums", className)}>
```

and the matching closing `</div>` (the last line of the returned JSX) with `</span>`. Update the comment above the component to say "the quiet line in a page header's subtitle".

Replace the whole of `web/src/pages/monitoring.tsx` with:

```tsx
import { useMemo, useState } from "react";
import { HardDrive, Server as ServerIcon } from "lucide-react";

import { FiringBanner } from "@/components/alerts/firing-banner";
import { TargetAlertsCard } from "@/components/alerts/target-alerts-card";
import { ChartEmpty } from "@/components/chart-parts";
import { DataFreshness } from "@/components/data-freshness";
import { Disclosure } from "@/components/disclosure";
import { EmptyState } from "@/components/empty-state";
import { KpiTile, KpiTileSkeleton } from "@/components/kpi-tile";
import { ChartCardSkeleton, MetricChartCard, type ChartSeries } from "@/components/metric-chart";
import { PageHeader } from "@/components/page-header";
import { RangeMenu } from "@/components/range-menu";
import { Section } from "@/components/section";
import { Card } from "@/components/ui/card";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { useMetricsOverview, useServerMetrics } from "@/hooks/use-metrics";
import { appLabel, deltaLabel, isSignificant, kpiNow, latestValue, percentCeiling, periodDelta, withNetTotal } from "@/lib/kpi";
import {
  chartColor,
  dockerDiskUsage,
  formatPercent,
  formatRate,
  formatSize,
  formatTemp,
  fullestDisk,
  lastPoint,
  stackApps,
  temperatureChart,
  toChartRows,
  type ChartRow,
  type DockerDF,
  type MetricPoint,
  type MetricsMode,
  type MetricsRange,
} from "@/lib/metrics";

// withPercents adds memory and swap as percentages of their totals (avg and
// peak), so both chart on one 0-100 axis.
function withPercents(points: MetricPoint[]): MetricPoint[] {
  return points.map((p) => {
    const out = { ...p };
    if (p.mem_total) {
      out.mem_pct = (100 * p.mem_used) / p.mem_total;
      out.mem_pct_max = (100 * (p.mem_used_max ?? p.mem_used)) / p.mem_total;
    }
    if (p.swap_total) {
      out.swap_pct = (100 * p.swap_used) / p.swap_total;
      out.swap_pct_max = (100 * (p.swap_used_max ?? p.swap_used)) / p.swap_total;
    }
    return out;
  });
}

function s(key: string, label: string, i: number): ChartSeries {
  return { key, label, color: chartColor(i) };
}

// Two compact tiles per row on a phone, four from lg.
const KPI_GRID = "grid grid-cols-2 gap-3 lg:grid-cols-4";
// One chart per row below md, two from md. Fixed rows with plots that fill
// them: cards in a row end level whether or not they carry a legend.
const CHART_GRID = "grid auto-rows-[18.5rem] gap-3 md:grid-cols-2";
const HOST_CHARTS = ["CPU", "Memory", "Disk usage", "Network"];

// DockerDisk is the hourly Docker disk scan: one proportion bar of images,
// volumes and build cache, then the figures, reclaimable last.
function DockerDisk({ df }: { df: DockerDF | undefined }) {
  const parts = df
    ? [
        { label: "Images", value: df.images, color: chartColor(0) },
        { label: "Volumes", value: df.volumes, color: chartColor(1) },
        { label: "Build cache", value: df.build_cache, color: chartColor(2) },
      ]
    : [];
  const total = parts.reduce((a, p) => a + p.value, 0);
  return (
    <Card data-slot="widget-card" className="flex min-w-0 flex-col gap-3 p-3.5">
      <div className="flex min-h-5 items-center gap-2">
        <h3 className="truncate text-sm font-medium text-muted-foreground">Docker disk usage</h3>
        {df ? (
          <span className="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums">{formatSize(total)} total</span>
        ) : null}
      </div>
      {df ? (
        <>
          {total > 0 ? (
            <div className="flex h-2 gap-0.5 overflow-hidden rounded-full bg-muted" aria-hidden>
              {parts.map((p) =>
                p.value > 0 ? (
                  <div key={p.label} style={{ width: `${(100 * p.value) / total}%`, backgroundColor: p.color }} />
                ) : null,
              )}
            </div>
          ) : null}
          <dl className="grid grid-cols-2 gap-x-3 gap-y-3 sm:grid-cols-4" data-testid="docker-df">
            {parts.map((p) => (
              <div key={p.label} className="min-w-0">
                <dt className="flex items-center gap-1.5 text-xs text-muted-foreground">
                  <span aria-hidden className="size-2 shrink-0 rounded-full" style={{ backgroundColor: p.color }} />
                  {p.label}
                </dt>
                <dd className="mt-1 text-xl font-semibold tracking-tight tabular-nums">{formatSize(p.value)}</dd>
              </div>
            ))}
            <div className="min-w-0">
              <dt className="text-xs text-muted-foreground">Reclaimable</dt>
              <dd className="mt-1 text-xl font-semibold tracking-tight text-muted-foreground tabular-nums">
                {formatSize(df.reclaimable)}
              </dd>
            </div>
          </dl>
        </>
      ) : (
        <ChartEmpty icon={HardDrive} className="py-6" message="No disk scan yet. It runs once an hour." />
      )}
    </Card>
  );
}

// MonitoringPage charts the active server (spec §5): firing alerts on top,
// four KPI tiles with the range's trend, the four host charts, what each app
// uses, then Load, Disk I/O, Temperature and Docker's disk usage under "More
// details" (not drawn until opened), and the server's alert rules collapsed.
export default function MonitoringPage() {
  const { activeServer, activeServerId } = useServers();
  const { apps } = useApps();
  // 1h is served from raw samples, so it has data from the first minute;
  // coarser ranges fill in once their rollup buckets close.
  const [range, setRange] = useState<MetricsRange>("1h");
  const [mode, setMode] = useState<MetricsMode>("avg");
  const { data, loading, error, updatedAt, refresh } = useServerMetrics(activeServerId, range);
  const { data: overview, refresh: refreshOverview } = useMetricsOverview();

  const points = useMemo(() => withNetTotal(withPercents(data?.points ?? [])), [data]);

  if (!activeServerId) {
    return (
      <div className="space-y-6">
        <PageHeader title="Monitoring" />
        <EmptyState icon={ServerIcon} title="No server selected." />
      </div>
    );
  }

  const serverName = activeServer?.name ?? "Server";
  const interval = data?.interval_seconds ?? 60;
  const rows = (fields: string[]) => toChartRows(points, interval, fields, mode);
  const temps = temperatureChart(points, interval, mode);
  const appSeries = (data?.apps ?? []).map((a, i) =>
    s(a.app_id, appLabel(apps.find((x) => x.id === a.app_id)?.name, a.app_id), i),
  );
  // The disk scan is hourly: take the latest sample's, not the range's.
  const latest = overview?.servers?.find((o) => o.server_id === activeServerId)?.latest?.server;
  const df = dockerDiskUsage(latest?.docker_df, points);
  // Peaks of different apps do not happen at once: summing them overstates
  // the total, so in peak mode the apps are drawn as separate lines.
  const peak = mode === "peak";

  const initial = loading && !data;
  const last = lastPoint(points);
  const disk = fullestDisk(latest?.disks);
  const cpuRows = rows(["cpu", "load1"]);
  const memRows = rows(["mem_pct"]);
  const diskRows = rows(["disk_pct"]);
  const netRows = rows(["net_bps", "net_rx_bps", "net_tx_bps"]);
  // Headline figures are live (the overview's latest sample) on every range;
  // the sparkline and the delta describe the range.
  const now = kpiNow(latest, {
    cpu: latestValue(cpuRows, "cpu"),
    mem_pct: latestValue(memRows, "mem_pct"),
    disk_pct: latestValue(diskRows, "disk_pct"),
    net_bps: latestValue(netRows, "net_bps"),
    net_rx_bps: latestValue(netRows, "net_rx_bps"),
    net_tx_bps: latestValue(netRows, "net_tx_bps"),
    load1: latestValue(cpuRows, "load1"),
  });
  const vs = deltaLabel(range);
  const delta = (r: ChartRow[], field: string, unit: "percent" | "amount") => {
    const d = periodDelta(r, field, range, interval);
    return { delta: d?.percent ?? null, significant: d ? isSignificant(d, unit) : true };
  };
  const memTotal = latest?.mem_total ?? last?.mem_total;
  const memUsed = latest?.mem_total ? latest.mem_used : last?.mem_used;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Monitoring"
        subtitle={
          <span className="inline-flex flex-wrap items-center gap-x-1.5">
            <span className="font-medium text-foreground">{serverName}</span>
            <span aria-hidden>·</span>
            <DataFreshness updatedAt={updatedAt} onRefresh={() => Promise.all([refresh(), refreshOverview()])} />
          </span>
        }
        actions={<RangeMenu range={range} onRangeChange={setRange} mode={mode} onModeChange={setMode} />}
      />
      <FiringBanner serverId={activeServerId} />
      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      {initial ? (
        <>
          <div className={KPI_GRID}>
            {["CPU", "Memory", "Disk", "Network"].map((l) => (
              <KpiTileSkeleton key={l} label={l} />
            ))}
          </div>
          <div className={CHART_GRID}>
            {HOST_CHARTS.map((t) => (
              <ChartCardSkeleton key={t} title={t} className="h-full" />
            ))}
          </div>
        </>
      ) : (
        <>
          {/* Percent sparks run from zero to a round ceiling: a disk at 56.4 %
              wobbling by 0.2 % must not draw as a cliff. */}
          <div className={KPI_GRID} data-testid="kpi-grid">
            <KpiTile
              label="CPU"
              value={formatPercent(now.cpu)}
              {...delta(cpuRows, "cpu", "percent")}
              deltaLabel={vs}
              goodWhen="down"
              sub={now.load1 !== undefined ? `Load ${now.load1.toFixed(2)}` : undefined}
              spark={{ rows: cpuRows, field: "cpu", color: chartColor(0), max: percentCeiling(cpuRows, "cpu") }}
            />
            <KpiTile
              label="Memory"
              value={formatPercent(now.mem_pct)}
              {...delta(memRows, "mem_pct", "percent")}
              deltaLabel={vs}
              goodWhen="down"
              sub={memTotal ? `${formatSize(memUsed)} of ${formatSize(memTotal)}` : undefined}
              spark={{ rows: memRows, field: "mem_pct", color: chartColor(1), max: percentCeiling(memRows, "mem_pct") }}
            />
            <KpiTile
              label="Disk"
              value={formatPercent(now.disk_pct)}
              {...delta(diskRows, "disk_pct", "percent")}
              deltaLabel={vs}
              goodWhen="down"
              sub={disk ? `${formatSize(disk.used)} of ${formatSize(disk.total)} · ${disk.mount}` : undefined}
              spark={{ rows: diskRows, field: "disk_pct", color: chartColor(2), max: percentCeiling(diskRows, "disk_pct") }}
            />
            <KpiTile
              label="Network"
              value={formatRate(now.net_bps)}
              {...delta(netRows, "net_bps", "amount")}
              deltaLabel={vs}
              goodWhen="neutral"
              sub={
                now.net_bps !== undefined
                  ? `In ${formatRate(now.net_rx_bps)} · Out ${formatRate(now.net_tx_bps)}`
                  : undefined
              }
              spark={{ rows: netRows, field: "net_bps", color: chartColor(4) }}
            />
          </div>

          <div className={CHART_GRID} data-testid="host-charts">
            <MetricChartCard
              height="fill"
              title="CPU"
              rows={rows(["cpu"])}
              series={[s("cpu", "CPU", 0)]}
              format={formatPercent}
              range={range}
            />
            <MetricChartCard
              height="fill"
              title="Memory"
              rows={rows(["mem_pct", "swap_pct"])}
              series={[s("mem_pct", "Memory", 1), s("swap_pct", "Swap", 3)]}
              format={formatPercent}
              range={range}
            />
            <MetricChartCard
              height="fill"
              title="Disk usage"
              description="Fullest filesystem"
              rows={diskRows}
              series={[s("disk_pct", "Used", 2)]}
              format={formatPercent}
              range={range}
            />
            <MetricChartCard
              height="fill"
              title="Network"
              rows={rows(["net_rx_bps", "net_tx_bps"])}
              series={[s("net_rx_bps", "In", 1), s("net_tx_bps", "Out", 3)]}
              format={formatRate}
              range={range}
            />
          </div>

          <Section title="Apps">
            <div className={CHART_GRID} data-testid="app-charts">
              <MetricChartCard
                height="fill"
                title="CPU by app"
                rows={stackApps(data?.apps ?? [], "cpu", mode)}
                series={appSeries}
                format={formatPercent}
                range={range}
                stacked={!peak}
                lines={peak}
              />
              <MetricChartCard
                height="fill"
                title="Memory by app"
                rows={stackApps(data?.apps ?? [], "mem_used", mode)}
                series={appSeries}
                format={formatSize}
                range={range}
                stacked={!peak}
                lines={peak}
              />
            </div>
          </Section>

          {/* Collapsed by default: these charts are not drawn until opened. */}
          <Disclosure label="More details">
            <div className={CHART_GRID}>
              <MetricChartCard
                height="fill"
                title="Load"
                rows={rows(["load1", "load5", "load15"])}
                series={[s("load1", "1 min", 0), s("load5", "5 min", 1), s("load15", "15 min", 2)]}
                format={(v) => v.toFixed(2)}
                scale="integer"
                range={range}
                lines
              />
              <MetricChartCard
                height="fill"
                title="Disk I/O"
                rows={rows(["disk_read_bps", "disk_write_bps"])}
                series={[s("disk_read_bps", "Read", 0), s("disk_write_bps", "Write", 1)]}
                format={formatRate}
                range={range}
              />
              {/* No temperature sensors: the chart is hidden, not empty. */}
              {temps.series.length > 0 ? (
                <MetricChartCard
                  height="fill"
                  title="Temperature"
                  rows={temps.rows}
                  series={temps.series}
                  format={formatTemp}
                  range={range}
                  lines
                  className="md:col-span-2"
                />
              ) : null}
            </div>
            <DockerDisk df={df} />
          </Disclosure>
        </>
      )}

      <TargetAlertsCard serverId={activeServerId} targetLabel={activeServer?.name ?? "this server"} />
    </div>
  );
}
```

Then check the page for leftovers from earlier phases. If phase 1's `usePageTitle("Monitoring")` call or its import survived in a merge, drop them: `PageHeader` names the page. `useServerPath` is no longer imported.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/pages/monitoring.test.tsx src/components/app-metrics.test.tsx`
Expected: PASS. `app-metrics` still renders `DataFreshness`, now an inline-flex span in the same flex row.

Run: `grep -rnE 'METRICS_|_INTERVAL|_ENABLED|SHOUTRRR' web/src/pages/monitoring.tsx web/src/pages/inbox.tsx web/src/components/alerts/ web/src/components/data-freshness.tsx`
Expected: no output (no env var names in UI strings).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/data-freshness.tsx web/src/pages/monitoring.tsx web/src/pages/monitoring.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Monitoring with firing banner, four charts, apps, and details on demand

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Phase verification

**Files:** none change unless a check fails. If one does, fix it in the owning task's files and commit as `fix(web): …` with the `Co-Authored-By` line.

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

Expected: the web lines changed in this phase are at least 80% covered. If a file falls short, add the missing case to that task's test file, commit, and re-run.

- [ ] **Step 3: Nothing outside `web/` and `docs/` changed**

```bash
git diff --stat main -- . ':!web' ':!docs'
```

Expected: no output (no Go, no `api/openapi.yaml`).

- [ ] **Step 4: Browser check on a throwaway standalone instance**

Build and run a standalone binary against a scratch data directory. Use `make build`, then `bin/standalone` with `DATABASE_URL=sqlite://…` and `AGENT_DATA_DIR` in a temp dir (see `.env.example`). Sign in, deploy one small app, and in Settings → Notifications add a server CPU rule with a threshold of 1% for 1 minute so an alert fires. Check at 1280, 768 and 375 px, light and dark:
- Monitoring:
  - The header shows "Monitoring", the server name · "Data as of …" ⟳, and the range menu (which drops under the title on a phone).
  - The red banner "1 alert firing" appears once the rule fires.
  - Four tiles (two per row at 375), then CPU, Memory, Disk usage and Network (one column at 375), then "Apps" with the two by-app charts.
  - "More details" opens Load, Disk I/O, Temperature (on a host with sensors) and Docker disk usage.
  - "Alert rules for this server" opens the rule list with a "Firing" pill; "Manage rules" opens Settings → Notifications.
- App page → Metrics tab: "Alert rules for this app" is collapsed below the charts.
- Inbox:
  - "Firing now" sits on top; below it, Today with "homelab CPU above 1%", a red dot, the unread dot and "Not sent" (or "Sent to …").
  - Clicking a row opens the server's Monitoring page.
  - Firing / Resolved filter the list, and Resolved hides "Firing now".
  - "Mark all as read" clears the dots and the sidebar badge.
  - After disabling the rule, a resolved row ("… back to …%", green check) appears without a reload.
  - "Notification settings →" leads to Settings → Notifications.
- `/server/<id>/monitoring` and `/server/<id>/app/<app>?tab=metrics` opened directly (the alert e-mail links) land on these pages.

Take screenshots at the three widths for the PR description. Stop the instance and delete its scratch directory.

- [ ] **Step 5: Commit any fixes**

```bash
git status --short
```

Expected: the tree is clean, or the only changes are the fixes, committed as `fix(web): …` with the `Co-Authored-By` line.

---

## Interfaces for later phases

- `@/lib/alert-text`: `alertTitle`, `eventTitle`, `activeTitle`, `targetText`, `deliveryNote`, `dayLabel`, `groupByDay`. Use these wherever an alert is shown in plain words (phase 7's polish may reuse them for the toasts in `alert-toasts.tsx`, which still show the notification titles).
- `FiringBanner({ serverId, appId? })` (`@/components/alerts/firing-banner`): the app page header may use it with `appId` for its "firing-alert banner only when relevant".
- `TargetAlertsCard({ serverId, appId?, targetLabel, defaultOpen? })`: same call sites as before, now a collapsed "Alert rules for this server/app".
- `useAlertEvents(kind, serverId)` (`@/hooks/use-alert-events`): the paged alert history; `HistoryTab` no longer exists.
