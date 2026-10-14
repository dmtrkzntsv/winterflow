import { useEffect, useState } from "react";

// useNow is the current time in ms, re-read every periodMs, for relative
// ages ("offline 5m") that must keep counting without new data. A coarse
// period keeps the idle cost to one re-render a minute; enabled false (no
// age on screen needs counting) stops the timer altogether.
export function useNow(periodMs = 60_000, enabled = true): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!enabled) return;
    const id = window.setInterval(() => setNow(Date.now()), periodMs);
    return () => window.clearInterval(id);
  }, [periodMs, enabled]);
  return now;
}
