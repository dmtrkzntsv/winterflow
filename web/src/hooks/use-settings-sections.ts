import { useMemo } from "react";

import { useProfile } from "@/context/use-profile";
import { useServers } from "@/context/use-servers";
import { settingsSections, type SettingsSection } from "@/lib/settings-nav";

// useSettingsSections is the Settings sub-menu for the active server and
// the signed-in account.
export function useSettingsSections(): SettingsSection[] {
  const { activeServer } = useServers();
  const { profile } = useProfile();
  const id = activeServer?.id ?? null;
  const name = activeServer?.name ?? "";
  const hasIngress = Boolean(activeServer?.features?.ingress);
  const hasPassword = Boolean(profile?.email);
  return useMemo(
    () => settingsSections({ server: id ? { id, name, hasIngress } : null, hasPassword }),
    [id, name, hasIngress, hasPassword],
  );
}
