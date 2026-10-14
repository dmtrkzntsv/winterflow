import { useContext, useEffect, useState } from "react";

import { apiBase } from "@/config";
import { AppsContext } from "@/context/apps-context-base";

type Listed = { data?: { apps?: { id: string; name: string }[] | null } };

// useAppNames are the current names of the apps on serverIds, by app id:
// read from each server's catalog (a database read, no agent hop), the
// active server's live list on top. An app absent from both is gone (or not
// read yet): callers fall back to a name of their own.
export function useAppNames(serverIds: readonly string[]): ReadonlyMap<string, string> {
  const live = useContext(AppsContext)?.apps;
  const [read, setRead] = useState<Record<string, string>>({});
  const key = [...new Set(serverIds)].sort().join(",");

  useEffect(() => {
    if (!key) return;
    let on = true;
    void Promise.all(
      key.split(",").map(async (id) => {
        try {
          const res = await fetch(`${apiBase}/api/v1/app/get-apps?server_id=${encodeURIComponent(id)}`, {
            credentials: "include",
          });
          if (!res.ok) return [];
          return ((await res.json()) as Listed).data?.apps ?? [];
        } catch {
          return [];
        }
      }),
    ).then((lists) => {
      if (on) setRead(Object.fromEntries(lists.flat().map((a) => [a.id, a.name])));
    });
    return () => {
      on = false;
    };
  }, [key]);

  const names = new Map(Object.entries(read));
  for (const a of live ?? []) names.set(a.id, a.name);
  return names;
}
