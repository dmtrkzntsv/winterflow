import { useCallback, useEffect } from "react";
import { useSearchParams } from "react-router-dom";

// useTabParam keeps a page's tab in the URL (?tab=, or opts.param), so a
// link or a reload opens the same tab. A missing or unknown value is the
// fallback tab, which stays out of the URL; an old name listed in
// opts.aliases opens its new tab and is rewritten in place.
export function useTabParam<T extends string>(
  tabs: readonly T[],
  fallback: T,
  opts: { param?: string; aliases?: Readonly<Record<string, T>> } = {},
): [T, (next: T) => void] {
  const param = opts.param ?? "tab";
  const [params, setParams] = useSearchParams();
  const raw = params.get(param);
  // Own keys only: ?tab=constructor must not resolve to Object.prototype.
  const aliased = raw !== null && opts.aliases && Object.hasOwn(opts.aliases, raw) ? opts.aliases[raw] : undefined;
  const wanted = aliased ?? raw;
  const tab = wanted !== null && (tabs as readonly string[]).includes(wanted) ? (wanted as T) : fallback;

  useEffect(() => {
    if (aliased === undefined) return;
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set(param, aliased);
        return next;
      },
      { replace: true },
    );
  }, [aliased, param, setParams]);

  const setTab = useCallback(
    (next: T) => {
      setParams(
        (prev) => {
          const out = new URLSearchParams(prev);
          if (next === fallback) out.delete(param);
          else out.set(param, next);
          return out;
        },
        { replace: true },
      );
    },
    [fallback, param, setParams],
  );

  return [tab, setTab];
}
