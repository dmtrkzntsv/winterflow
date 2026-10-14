import { useCallback, useEffect } from "react";
import { useSearchParams } from "react-router-dom";

// useHistorySheet keeps the app page's History sheet in the URL
// (?history=1), so a link or a reload opens it. An old ?tab=history link
// (History was a tab) becomes ?tab=configuration&history=1. Call it after
// useTabParam: both rewrite the URL on the first render, and the last
// rewrite wins, so this one must come last.
export function useHistorySheet(): [boolean, (open: boolean) => void] {
  const [params, setParams] = useSearchParams();
  const legacy = params.get("tab") === "history";
  const open = legacy || params.get("history") === "1";

  useEffect(() => {
    if (!legacy) return;
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", "configuration");
        next.set("history", "1");
        return next;
      },
      { replace: true },
    );
  }, [legacy, setParams]);

  const setOpen = useCallback(
    (next: boolean) => {
      setParams(
        (prev) => {
          const out = new URLSearchParams(prev);
          if (out.get("tab") === "history") out.set("tab", "configuration");
          if (next) out.set("history", "1");
          else out.delete("history");
          return out;
        },
        { replace: true },
      );
    },
    [setParams],
  );

  return [open, setOpen];
}
