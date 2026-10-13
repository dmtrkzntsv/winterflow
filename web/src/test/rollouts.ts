import type { Rollout } from "@/lib/rollouts";

// rollout is a running template upgrade of t1 (3 of 12: bakomi on s1),
// with over applied.
export function rollout(over: Partial<Rollout> = {}): Rollout {
  return {
    id: "r1",
    kind: "template_upgrade",
    scope_key: "t1",
    revision: 3,
    state: "running",
    stop_requested: false,
    done: 1,
    skipped: 1,
    total: 12,
    current_app_id: "a1b2c3d4e5f6",
    current_server_id: "s1",
    current_app_name: "bakomi",
    error: "",
    created_at: "2026-10-12T10:00:00Z",
    created_by: "u1",
    finished_at: null,
    ...over,
  };
}
