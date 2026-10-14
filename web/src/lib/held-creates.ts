import type { App } from "@/context/apps-context-base";

// The creates in flight, by server and name (a slot), for the whole session:
// a second Create of the same app while the first runs is ignored, even
// from a form that was left and opened again meanwhile, or from another tab
// (startCreateSync). A create that outlived its wait is still running on
// the agent (on a standalone server every other command waits behind it),
// so its slot stays claimed — across leaving the form — until its late
// result arrives, the app appears in the list, or HELD_CREATE_CAP_MS passes
// (the API forgets a pending command after 10 minutes; the cap is past
// that).
//
// A create's outcome is never lost with the form that sent it: it goes to
// a create form open for that server (attendCreates), else to the shell's
// listener (onUnattendedCreate), which tells the user in a toast.

export const HELD_CREATE_CAP_MS = 15 * 60_000;

// PendingCreate is a create of name on serverId: before are the app ids
// listed when it began; late, it outlived its wait (ref is its command's
// request id, "" when unknown); remote, another tab sent it.
export type PendingCreate = {
  slot: string;
  serverId: string;
  name: string;
  before: ReadonlySet<string>;
  ref: string;
  late: boolean;
  remote: boolean;
};

// CreateOutcome is how a create ended. list is the apps listed once it
// went through (its app among them when the list shows it). left is, for a
// failed create, the app it left behind once looked for (null: none);
// unset when nobody looked yet.
export type CreateOutcome = { failed: boolean; error: string; list?: App[]; left?: App | null };

type Handler = (create: PendingCreate, outcome: CreateOutcome) => void;

const entries = new Map<string, { create: PendingCreate; timer?: ReturnType<typeof setTimeout> }>();
const changeListeners = new Set<() => void>();
// attendants are the create forms open, per server, newest last.
const attendants: { serverId: string; fn: Handler }[] = [];
const unattended = new Set<Handler>();
let beforeExpiry: ((create: PendingCreate) => Promise<unknown>) | null = null;
// snapshot is the pending creates, newest last, as one array per change
// (for useSyncExternalStore).
let snapshot: PendingCreate[] = [];

function changed() {
  snapshot = [...entries.values()].map((e) => e.create);
  for (const fn of [...changeListeners]) fn();
}

export function createSlot(serverId: string, name: string): string {
  return `${serverId}\n${name}`;
}

// claimCreate takes the slot for a create about to be sent: null when a
// create of that name on that server is already in flight (here or in
// another tab).
export function claimCreate(serverId: string, name: string, before: ReadonlySet<string>): PendingCreate | null {
  const slot = createSlot(serverId, name);
  if (entries.has(slot)) return null;
  const create = { slot, serverId, name, before, ref: "", late: false, remote: false };
  entries.set(slot, { create });
  post({ kind: "claim", slot, serverId, name });
  changed();
  return create;
}

// releaseCreate frees this tab's slot (its create ended, or its held create
// did): false when it wasn't claimed here (already freed, or another tab's).
export function releaseCreate(slot: string): boolean {
  const e = entries.get(slot);
  if (!e || e.create.remote) return false;
  clearTimeout(e.timer);
  entries.delete(slot);
  post({ kind: "release", slot });
  changed();
  return true;
}

// holdCreate keeps a create that outlived its wait claimed, at most
// HELD_CREATE_CAP_MS; when the cap passes, the shell reads the agent's list
// once (onCreateExpiry) before the name is freed, so an app that did land
// is listed by then.
export function holdCreate(slot: string, ref: string): void {
  const e = entries.get(slot);
  if (!e || e.create.remote) return;
  clearTimeout(e.timer);
  const create = { ...e.create, ref, late: true };
  const timer = setTimeout(() => {
    const done = () => {
      if (entries.get(slot)?.create === create) releaseCreate(slot);
    };
    if (beforeExpiry) void beforeExpiry(create).catch(() => {}).finally(done);
    else done();
  }, HELD_CREATE_CAP_MS);
  entries.set(slot, { create, timer });
  changed();
}

// pendingCreates is every create in flight, newest last.
export function pendingCreates(): PendingCreate[] {
  return snapshot;
}

export function subscribePendingCreates(fn: () => void): () => void {
  changeListeners.add(fn);
  return () => void changeListeners.delete(fn);
}

// attendCreates is a create form open for serverId: the newest one hears
// how that server's creates ended when their own form is gone.
export function attendCreates(serverId: string, fn: Handler): () => void {
  const entry = { serverId, fn };
  attendants.push(entry);
  return () => {
    const i = attendants.indexOf(entry);
    if (i >= 0) attendants.splice(i, 1);
  };
}

// onUnattendedCreate hears the outcomes no create form is open for.
export function onUnattendedCreate(fn: Handler): () => void {
  unattended.add(fn);
  return () => void unattended.delete(fn);
}

// onCreateExpiry runs before a held create's name is freed at the cap.
export function onCreateExpiry(fn: (create: PendingCreate) => Promise<unknown>): () => void {
  beforeExpiry = fn;
  return () => {
    if (beforeExpiry === fn) beforeExpiry = null;
  };
}

// deliverCreate hands a create's outcome to a form open for its server,
// else to the shell's listener.
export function deliverCreate(create: PendingCreate, outcome: CreateOutcome): void {
  const attendant = attendants.filter((a) => a.serverId === create.serverId).at(-1);
  if (attendant) {
    attendant.fn(create, outcome);
    return;
  }
  for (const fn of [...unattended]) fn(create, outcome);
}

// settleLateCreate is a command's late result: when it is a held create's,
// its slot is freed and its outcome delivered. Anything else is ignored.
export function settleLateCreate(ref: string, outcome: CreateOutcome): void {
  if (!ref) return;
  const create = snapshot.find((c) => c.late && !c.remote && c.ref === ref);
  if (!create || !releaseCreate(create.slot)) return;
  deliverCreate(create, outcome);
}

// resetHeldCreates forgets everything (tests).
export function resetHeldCreates(): void {
  for (const e of entries.values()) clearTimeout(e.timer);
  entries.clear();
  attendants.length = 0;
  unattended.clear();
  beforeExpiry = null;
  channel?.close();
  channel = null;
  changed();
}

// Tabs share their claims over a BroadcastChannel: a tab hears the others'
// claims and releases (and asks for the claims made before it opened). A
// claim whose tab went away without releasing it lapses after the cap.

type SyncMessage = { kind: "claim"; slot: string; serverId: string; name: string } | { kind: "release"; slot: string } | { kind: "sync" };

// SyncChannel is the part of BroadcastChannel used here.
export type SyncChannel = {
  postMessage: (message: SyncMessage) => void;
  onmessage: ((event: MessageEvent<SyncMessage>) => void) | null;
  close: () => void;
};

let channel: SyncChannel | null = null;

function post(message: SyncMessage) {
  try {
    channel?.postMessage(message);
  } catch {
    // Another tab not hearing it only lets it create the name twice.
  }
}

function heard(message: SyncMessage | undefined) {
  if (!message || typeof message !== "object") return;
  if (message.kind === "sync") {
    for (const { create } of entries.values()) {
      if (!create.remote) post({ kind: "claim", slot: create.slot, serverId: create.serverId, name: create.name });
    }
    return;
  }
  const e = entries.get(message.slot);
  if (message.kind === "release") {
    if (!e?.create.remote) return;
    clearTimeout(e.timer);
    entries.delete(message.slot);
    changed();
    return;
  }
  if (e || message.kind !== "claim") return;
  const create = { slot: message.slot, serverId: message.serverId, name: message.name, before: new Set<string>(), ref: "", late: true, remote: true };
  const timer = setTimeout(() => {
    if (entries.get(message.slot)?.create === create) {
      entries.delete(message.slot);
      changed();
    }
  }, HELD_CREATE_CAP_MS);
  entries.set(message.slot, { create, timer });
  changed();
}

// startCreateSync shares this tab's claims with the others (once, at
// start); without BroadcastChannel each tab only knows its own.
export function startCreateSync(open: () => SyncChannel = () => new BroadcastChannel("winterflow-creates") as unknown as SyncChannel): void {
  if (channel) return;
  try {
    channel = open();
  } catch {
    channel = null;
    return;
  }
  channel.onmessage = (event) => heard(event.data);
  post({ kind: "sync" });
}
