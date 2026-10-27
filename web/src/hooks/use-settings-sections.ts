import { useEffect, useMemo, useState } from "react";

import { useProfile } from "@/context/use-profile";
import { useServers } from "@/context/use-servers";
import { loadAuthState } from "@/lib/auth-state";
import { settingsSections, type SettingsSection } from "@/lib/settings-nav";

// useSettingsSections is the Settings sub-menu for the active server and
// the signed-in account (the instance's auth state says whether sign-in
// providers and connected apps exist; read once per page load).
export function useSettingsSections(): SettingsSection[] {
  const { activeServer } = useServers();
  const { profile } = useProfile();
  const [auth, setAuth] = useState({ hasProviders: false, oauthServer: false });
  useEffect(() => {
    let cancelled = false;
    loadAuthState().then(
      (s) => {
        if (!cancelled) setAuth({ hasProviders: s.providers.length > 0, oauthServer: s.oauthServer });
      },
      () => {},
    );
    return () => {
      cancelled = true;
    };
  }, []);
  const id = activeServer?.id ?? null;
  const name = activeServer?.name ?? "";
  const hasIngress = Boolean(activeServer?.features?.ingress);
  const hasPassword = Boolean(profile?.email);
  const { hasProviders, oauthServer } = auth;
  return useMemo(
    () => settingsSections({ server: id ? { id, name, hasIngress } : null, hasPassword, hasProviders, oauthServer }),
    [id, name, hasIngress, hasPassword, hasProviders, oauthServer],
  );
}
