import { useContext, useEffect, useState } from "react";

import { apiBase } from "@/config";
import { AppsContext } from "@/context/apps-context-base";
import { createJsonClient } from "@/lib/api";

type Listed = { apps?: { id: string; name: string }[] | null };

const api = createJsonClient(apiBase);

// useAppNames are the current names of the apps on serverIds, by app id:
// read from each server's catalog (a database read, no agent hop), the
// active server's live list on top, and read again when that list changes
// (an app renamed, added or deleted). An app absent from both is gone (or
// not read yet): callers fall back to a name of their own.
export function useAppNames(serverIds: readonly string[]): ReadonlyMap<string, string> {
  const live = useContext(AppsContext)?.apps;
  const [read, setRead] = useState<Record<string, string>>({});
  const key = [...new Set(serverIds)].sort().join(",");
  const listed = (live ?? []).map((a) => `${a.id}\n${a.name}`).join("\n");

  useEffect(() => {
    if (!key) return;
    let on = true;
    void Promise.all(
      key.split(",").map((id) =>
        api
          .call<Listed>(`/api/v1/app/get-apps?${new URLSearchParams({ server_id: id })}`)
          .then((d) => d?.apps ?? [])
          .catch(() => []),
      ),
    ).then((lists) => {
      if (on) setRead(Object.fromEntries(lists.flat().map((a) => [a.id, a.name])));
    });
    return () => {
      on = false;
    };
  }, [key, listed]);

  const names = new Map(Object.entries(read));
  for (const a of live ?? []) names.set(a.id, a.name);
  return names;
}
