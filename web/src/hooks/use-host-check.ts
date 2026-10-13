import { useEffect, useRef, useState } from "react";

import type { HostCheck } from "@/components/host-hints";
import { checkHostname } from "@/lib/host-check";

type Options = {
  // enabled: the host is worth asking about (valid syntax, a server).
  enabled: boolean;
  // delay debounces the check (ms), or says the debounce when the check
  // is scheduled (a function may read refs); a change alone schedules none.
  delay: number | (() => number);
  // recheck, when it changes, asks again about the same host.
  recheck?: number;
  // onResult sees each answer (the latest callback is used).
  onResult?: (check: HostCheck) => void;
};

// useHostCheck runs the live check of a hostname on a server and returns
// the answer while the hostname is the one it was for (null otherwise: once
// the text changes the old answer no longer shows, with nothing to clear).
// A newer check, or the component going away, aborts the one in flight;
// a failed check is advisory and shows nothing.
export function useHostCheck(
  serverId: string,
  appId: string | undefined,
  host: string,
  { enabled, delay, recheck = 0, onResult }: Options,
): HostCheck | null {
  const [result, setResult] = useState<{ host: string; check: HostCheck } | null>(null);
  const report = useRef(onResult);
  const wait = useRef(delay);
  useEffect(() => {
    report.current = onResult;
    wait.current = delay;
  });

  useEffect(() => {
    if (!enabled) return;
    const ctl = new AbortController();
    const timer = setTimeout(() => {
      checkHostname(serverId, appId, host, ctl.signal).then(
        (check) => {
          if (ctl.signal.aborted || !check) return;
          setResult({ host, check });
          report.current?.(check);
        },
        () => {},
      );
    }, typeof wait.current === "function" ? wait.current() : wait.current);
    return () => {
      clearTimeout(timer);
      ctl.abort();
    };
  }, [enabled, serverId, appId, host, recheck]);

  return result?.host === host ? result.check : null;
}
