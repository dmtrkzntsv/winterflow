import { afterEach, describe, expect, it, vi } from "vitest";

import {
  HELD_CREATE_CAP_MS,
  attendCreates,
  claimCreate,
  deliverCreate,
  holdCreate,
  onUnattendedCreate,
  pendingCreates,
  releaseCreate,
  resetHeldCreates,
  startCreateSync,
  type SyncChannel,
} from "@/lib/held-creates";

// peer is another tab on the channel: what this tab posts, and a way to
// post to it.
function peer() {
  const posted: unknown[] = [];
  const channel: SyncChannel = { postMessage: (m) => posted.push(m), onmessage: null, close: vi.fn() };
  startCreateSync(() => channel);
  const say = (data: unknown) => channel.onmessage?.({ data } as MessageEvent);
  return { posted, say, channel };
}

describe("held creates", () => {
  afterEach(() => vi.useRealTimers());

  it("delivers an outcome to the newest form open for its server, else to the shell", () => {
    const c = claimCreate("s1", "shop", new Set())!;
    const first = vi.fn();
    const second = vi.fn();
    const other = vi.fn();
    const shell = vi.fn();
    onUnattendedCreate(shell);
    const stopFirst = attendCreates("s1", first);
    const stopSecond = attendCreates("s1", second);
    attendCreates("s2", other);
    deliverCreate(c, { failed: false, error: "" });
    expect(second).toHaveBeenCalledTimes(1);
    expect(first).not.toHaveBeenCalled();
    stopSecond();
    stopSecond();
    stopFirst();
    deliverCreate(c, { failed: false, error: "" });
    expect(shell).toHaveBeenCalledTimes(1);
    expect(other).not.toHaveBeenCalled();
  });

  it("frees a held create at the cap without a listener to read the list first", () => {
    vi.useFakeTimers();
    const c = claimCreate("s1", "shop", new Set())!;
    holdCreate(c.slot, "r1");
    holdCreate("unknown", "r2");
    vi.advanceTimersByTime(HELD_CREATE_CAP_MS);
    expect(pendingCreates()).toEqual([]);
  });

  it("tells the other tabs of its claims and releases, and answers a new tab's question", () => {
    const { posted, say } = peer();
    expect(posted).toEqual([{ kind: "sync" }]);
    const c = claimCreate("s1", "shop", new Set())!;
    expect(posted.at(-1)).toEqual({ kind: "claim", slot: c.slot, serverId: "s1", name: "shop" });
    say({ kind: "sync" });
    expect(posted.at(-1)).toEqual({ kind: "claim", slot: c.slot, serverId: "s1", name: "shop" });
    releaseCreate(c.slot);
    expect(posted.at(-1)).toEqual({ kind: "release", slot: c.slot });
  });

  it("refuses a name another tab claimed until that tab frees it, or the cap passes", () => {
    vi.useFakeTimers();
    const { say } = peer();
    say({ kind: "claim", slot: "s1\nshop", serverId: "s1", name: "shop" });
    expect(pendingCreates()).toEqual([expect.objectContaining({ name: "shop", remote: true })]);
    expect(claimCreate("s1", "shop", new Set())).toBeNull();
    // Only its tab frees it.
    expect(releaseCreate("s1\nshop")).toBe(false);
    say({ kind: "claim", slot: "s1\nshop", serverId: "s1", name: "shop" });
    expect(pendingCreates()).toHaveLength(1);
    say({ kind: "release", slot: "s1\nshop" });
    expect(pendingCreates()).toEqual([]);
    // A tab that went away without freeing it: the claim lapses.
    say({ kind: "claim", slot: "s1\nblog", serverId: "s1", name: "blog" });
    vi.advanceTimersByTime(HELD_CREATE_CAP_MS);
    expect(pendingCreates()).toEqual([]);
  });

  it("ignores a release of its own claim from another tab, and junk", () => {
    const { say } = peer();
    const c = claimCreate("s1", "shop", new Set())!;
    say({ kind: "release", slot: c.slot });
    say(undefined);
    say("hello");
    expect(pendingCreates()).toEqual([c]);
  });

  it("shares claims over a BroadcastChannel by default", () => {
    const opened: string[] = [];
    vi.stubGlobal(
      "BroadcastChannel",
      class {
        onmessage = null;
        postMessage = vi.fn();
        close = vi.fn();
        constructor(name: string) {
          opened.push(name);
        }
      },
    );
    try {
      startCreateSync();
      startCreateSync();
      expect(opened).toEqual(["winterflow-creates"]);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("works alone when the tabs can't share claims", () => {
    startCreateSync(() => {
      throw new Error("no BroadcastChannel");
    });
    const { posted, channel } = peer();
    channel.postMessage = () => {
      throw new Error("closed");
    };
    expect(posted).toEqual([{ kind: "sync" }]);
    expect(claimCreate("s1", "shop", new Set())).not.toBeNull();
    resetHeldCreates();
    expect(channel.close).toHaveBeenCalled();
  });
});
