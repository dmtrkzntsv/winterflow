import { useEffect, useState } from "react";

// useNow is the current time in ms, re-read every periodMs, for relative
// ages ("offline 5m") that must keep counting without new data. A coarse
// period keeps the idle cost to one re-render a minute; enabled false (no
// age on screen needs counting) stops the timer altogether.
export function useNow(periodMs = 60_000, enabled = true): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!enabled) return;
    const tick = () => setNow(Date.now());
    // Restarted (enabled again, or a new period): the time kept from
    // before may be long past, so it is read again at once.
    const first = window.setTimeout(tick, 0);
    const id = window.setInterval(tick, periodMs);
    return () => {
      window.clearTimeout(first);
      window.clearInterval(id);
    };
  }, [periodMs, enabled]);
  return now;
}
