import { useCallback, useEffect, useState } from "react";

import { useTemplatesApi } from "@/hooks/use-templates-api";
import type { InstanceView, TemplateView } from "@/lib/templates";

export type AppInstance = {
  // loading is true until a templated app's instance is known: the page
  // must not offer the plain editor meanwhile (its save would be refused).
  loading: boolean;
  error: string;
  instance: InstanceView | null;
  // current is the instance's revision (its latest_revision tells whether
  // it is behind).
  current: TemplateView | null;
  reload: () => Promise<void>;
};

// useAppInstance finds the template instance that manages an app (the app
// list's template_id says one does): two reads, only for such an app.
export function useAppInstance(app: { id: string; serverId: string; templateId?: string } | undefined): AppInstance {
  const api = useTemplatesApi();
  const id = app?.id;
  const serverId = app?.serverId;
  const templateId = app?.templateId;
  const [loading, setLoading] = useState(Boolean(templateId));
  const [error, setError] = useState("");
  const [instance, setInstance] = useState<InstanceView | null>(null);
  const [current, setCurrent] = useState<TemplateView | null>(null);

  const reload = useCallback(async () => {
    setError("");
    if (!id || !serverId || !templateId) {
      setInstance(null);
      setCurrent(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const list = await api.listInstances({ serverId, templateId });
      const inst = list.find((i) => i.app_id === id) ?? null;
      setInstance(inst);
      setCurrent(inst ? await api.getTemplate(inst.template_id, inst.revision) : null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load the app's template");
    } finally {
      setLoading(false);
    }
  }, [api, id, serverId, templateId]);

  useEffect(() => {
    void reload();
  }, [reload]);

  return { loading, error, instance, current, reload };
}
