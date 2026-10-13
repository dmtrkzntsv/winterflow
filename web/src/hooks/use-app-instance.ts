import { useCallback, useEffect, useRef, useState } from "react";

import { useTemplatesApi } from "@/hooks/use-templates-api";
import type { InstanceView, TemplateView } from "@/lib/templates";

export type AppInstance = {
  // loading is true until a templated app's instance is known (also while
  // it is read again): the page must not offer the plain editor meanwhile
  // (its save would be refused).
  loading: boolean;
  error: string;
  instance: InstanceView | null;
  // current is the instance's revision (its latest_revision tells whether
  // it is behind).
  current: TemplateView | null;
  reload: () => Promise<void>;
};

type Looked = { key: string; instance: InstanceView | null; current: TemplateView | null; error: string };

// useAppInstance finds the template instance that manages an app (the app
// list's template_id says one does): two reads, only for such an app. The
// page stays mounted when the user moves from app to app, so a result is
// kept with the app it was read for, and a read that is no longer current
// is dropped.
export function useAppInstance(app: { id: string; serverId: string; templateId?: string } | undefined): AppInstance {
  const api = useTemplatesApi();
  const id = app?.id;
  const serverId = app?.serverId;
  const templateId = app?.templateId;
  const key = id && serverId && templateId ? `${serverId}/${id}/${templateId}` : "";
  const [looked, setLooked] = useState<Looked | null>(null);
  const [reading, setReading] = useState(false);
  const latest = useRef(0);

  // read looks the instance up; a read that is no longer the latest (the
  // user moved to another app, or asked again) is dropped.
  const read = useCallback(async () => {
    const n = ++latest.current;
    if (!id || !serverId || !templateId) return;
    const mine = `${serverId}/${id}/${templateId}`;
    const commit = (next: Looked) => {
      if (n !== latest.current) return;
      setLooked(next);
      setReading(false);
    };
    try {
      const list = await api.listInstances({ serverId, templateId });
      const inst = list.find((i) => i.app_id === id) ?? null;
      const current = inst ? await api.getTemplate(inst.template_id, inst.revision) : null;
      commit({ key: mine, instance: inst, current, error: "" });
    } catch (e) {
      const error = e instanceof Error ? e.message : "Failed to load the app's template";
      commit({ key: mine, instance: null, current: null, error });
    }
  }, [api, id, serverId, templateId]);

  // reload reads again on request; the page shows a spinner meanwhile.
  const reload = useCallback(() => {
    setReading(true);
    return read();
  }, [read]);

  useEffect(() => {
    void read();
  }, [read]);

  // Nothing read for this app yet counts as loading, so a templated app
  // never gets the plain editor on its first render.
  const fresh = key !== "" && looked?.key === key ? looked : null;
  return {
    loading: key !== "" && (reading || fresh === null),
    error: fresh?.error ?? "",
    instance: fresh?.instance ?? null,
    current: fresh?.current ?? null,
    reload,
  };
}
