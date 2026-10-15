import { useCallback, useEffect, useRef, useState } from "react";

import { apiBaseUrl } from "@/config";
import { WaitTimeoutError } from "@/context/notifications-context-base";
import { useNotifications } from "@/context/use-notifications";
import { readApiError } from "@/lib/api";
import type { ProcessList } from "@/lib/processes";

export const PROCESS_POLL_MS = 5000;
export const PROCESS_WAIT_MS = 15000;

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

// An agent that predates process.list answers every request with this.
const UNSUPPORTED = /unsupported command type/i;
const AGENT_TOO_OLD = "This server's agent is too old to list processes. Update the agent.";
// The agent gave up waiting for its /proc read (a hung process can hold
// one for good); the next poll waits for the same read again.
const READ_STUCK = /context deadline exceeded/i;

type State = { serverId: string | null; data: ProcessList | null; error: string | null; updatedAt: number | null };

// useProcesses polls the server's process list while enabled: one request
// at a time (dispatch, wait for its SSE result, then 5 s), so a slow agent
// never piles requests up. Disabling, unmounting or another server stops
// the loop; a reply that arrives after that is dropped. An agent that
// predates process.list stops it too: asking again cannot help until the
// agent is updated (another server or a remount starts over).
export function useProcesses(serverId: string | null, enabled: boolean) {
  const { waitFor } = useNotifications();
  const [state, setState] = useState<State>({ serverId: null, data: null, error: null, updatedAt: null });
  // wake ends the loop's current wait (Refresh); a no-op otherwise.
  const wake = useRef<() => void>(() => {});

  useEffect(() => {
    if (!serverId || !enabled) return;
    let live = true;
    const ctl = new AbortController();
    let timer: number | undefined;
    let resolveSleep: (() => void) | undefined;
    const sleep = (ms: number) =>
      new Promise<void>((r) => {
        resolveSleep = r;
        timer = window.setTimeout(r, ms);
      });
    wake.current = () => {
      window.clearTimeout(timer);
      resolveSleep?.();
    };
    const update = (patch: Partial<State>) =>
      setState((cur) => ({ ...(cur.serverId === serverId ? cur : { data: null, error: null, updatedAt: null }), ...patch, serverId }));

    void (async () => {
      while (live) {
        try {
          const res = await fetch(`${base}/api/v1/server/get-processes?server_id=${encodeURIComponent(serverId)}`, {
            credentials: "include",
            signal: ctl.signal,
          });
          if (!res.ok) throw await readApiError(res);
          const accepted = (await res.json()) as { data?: { request_id?: string } };
          const ref = accepted.data?.request_id;
          if (!ref) throw new Error("No request id");
          const n = await waitFor(ref, PROCESS_WAIT_MS);
          if (!live) return;
          if (n.status && n.status !== 0) {
            if (UNSUPPORTED.test(n.error ?? "")) {
              update({ error: AGENT_TOO_OLD });
              return;
            }
            if (READ_STUCK.test(n.error ?? "")) throw new Error("The server is still reading processes; retrying.");
            throw new Error(n.error || "Failed to list processes");
          }
          update({ data: n.payload as ProcessList, error: null, updatedAt: Date.now() });
        } catch (e) {
          if (!live) return;
          update({
            error: e instanceof WaitTimeoutError ? "The server did not answer in time." : e instanceof Error ? e.message : "Failed to list processes",
          });
        }
        if (!live) return;
        await sleep(PROCESS_POLL_MS);
      }
    })();

    return () => {
      live = false;
      ctl.abort();
      window.clearTimeout(timer);
      wake.current = () => {};
    };
  }, [serverId, enabled, waitFor]);

  const refresh = useCallback(() => wake.current(), []);
  const mine = state.serverId === serverId;
  return { data: mine ? state.data : null, error: mine ? state.error : null, updatedAt: mine ? state.updatedAt : null, refresh };
}
