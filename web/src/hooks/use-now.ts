import { useEffect, useState } from "react";

// useNow is the current time in ms, re-read every periodMs, for relative
// ages ("offline 5m") that must keep counting without new data. A coarse
// period keeps the idle cost to one re-render a minute.
export function useNow(periodMs = 60_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), periodMs);
    return () => window.clearInterval(id);
  }, [periodMs]);
  return now;
}
