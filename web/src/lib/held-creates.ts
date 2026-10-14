// The creates in flight, by server and name (a slot), for the whole session:
// a second Create of the same app while the first runs is ignored, even
// from a form that was left and opened again meanwhile. A create that
// outlived its wait is still running on the agent (on a standalone server
// every other command waits behind it), so its slot stays claimed — across
// leaving the form — until its late result arrives, the app appears in the
// list, or HELD_CREATE_CAP_MS passes (the API forgets a pending command
// after 10 minutes; the cap is past that).

export const HELD_CREATE_CAP_MS = 15 * 60_000;

// HeldCreate is a create that outlived its wait: ref is its command's
// request id ("" when unknown), before the app ids listed when it began.
export type HeldCreate = { slot: string; serverId: string; name: string; before: ReadonlySet<string>; ref: string };

// LateOutcome is what a held create's late result said.
export type LateOutcome = { failed: boolean; error: string };

const claimed = new Set<string>();
const held = new Map<string, { create: HeldCreate; timer: ReturnType<typeof setTimeout> }>();
const changeListeners = new Set<() => void>();
const lateListeners = new Set<(create: HeldCreate, outcome: LateOutcome) => void>();
// snapshot is the held creates, newest last, as one array per change (for
// useSyncExternalStore).
let snapshot: HeldCreate[] = [];

function changed() {
  snapshot = [...held.values()].map((h) => h.create);
  for (const fn of [...changeListeners]) fn();
}

export function createSlot(serverId: string, name: string): string {
  return `${serverId}\n${name}`;
}

// claimCreate takes a slot for a create about to be sent: false when a
// create of that name on that server is already in flight or held.
export function claimCreate(slot: string): boolean {
  if (claimed.has(slot)) return false;
  claimed.add(slot);
  return true;
}

// releaseCreate frees a slot (the create ended, or its held create did).
export function releaseCreate(slot: string): void {
  claimed.delete(slot);
  const h = held.get(slot);
  if (h) {
    clearTimeout(h.timer);
    held.delete(slot);
    changed();
  }
}

// holdCreate keeps a create that outlived its wait claimed, at most
// HELD_CREATE_CAP_MS.
export function holdCreate(create: HeldCreate): void {
  claimed.add(create.slot);
  const prior = held.get(create.slot);
  if (prior) clearTimeout(prior.timer);
  held.delete(create.slot);
  held.set(create.slot, { create, timer: setTimeout(() => releaseCreate(create.slot), HELD_CREATE_CAP_MS) });
  changed();
}

// heldCreates is every held create, newest last.
export function heldCreates(): HeldCreate[] {
  return snapshot;
}

export function subscribeHeldCreates(fn: () => void): () => void {
  changeListeners.add(fn);
  return () => void changeListeners.delete(fn);
}

// onLateCreate hears every held create's late result (after its slot is
// freed).
export function onLateCreate(fn: (create: HeldCreate, outcome: LateOutcome) => void): () => void {
  lateListeners.add(fn);
  return () => void lateListeners.delete(fn);
}

// settleLateCreate is a command's late result: when it is a held create's,
// its slot is freed and the listeners hear how it ended. Anything else is
// ignored.
export function settleLateCreate(ref: string, outcome: LateOutcome): void {
  if (!ref) return;
  const h = [...held.values()].find((x) => x.create.ref === ref);
  if (!h) return;
  releaseCreate(h.create.slot);
  for (const fn of [...lateListeners]) fn(h.create, outcome);
}

// resetHeldCreates forgets everything (tests).
export function resetHeldCreates(): void {
  for (const h of held.values()) clearTimeout(h.timer);
  held.clear();
  claimed.clear();
  changed();
}
