import {
  BellRing,
  Braces,
  Building2,
  Container,
  Globe,
  HardDrive,
  KeyRound,
  LockKeyhole,
  Server,
  Users,
  type LucideIcon,
} from "lucide-react";

import { serverSettingsPath, settingsPath } from "@/lib/routes";

export type SettingsItem = { label: string; to: string; icon: LucideIcon };
export type SettingsSection = { title: string; items: SettingsItem[] };

// settingsSections is the Settings sub-menu (spec §6): the active server's
// sections (Domains only with ingress), the organization's, and the
// account's (Password only for accounts with local credentials). The
// layout and the ⌘K palette both read it.
export function settingsSections({
  server,
  hasPassword,
}: {
  server: { id: string; name: string; hasIngress: boolean } | null;
  hasPassword: boolean;
}): SettingsSection[] {
  const sections: SettingsSection[] = [];
  if (server) {
    const items: SettingsItem[] = [{ label: "General", to: serverSettingsPath(server.id, "general"), icon: Server }];
    if (server.hasIngress) items.push({ label: "Domains", to: serverSettingsPath(server.id, "domains"), icon: Globe });
    items.push(
      { label: "Storages", to: serverSettingsPath(server.id, "storages"), icon: HardDrive },
      { label: "Docker", to: serverSettingsPath(server.id, "docker"), icon: Container },
    );
    sections.push({ title: `Server · ${server.name}`, items });
  }
  sections.push({
    title: "Organization",
    items: [
      { label: "General", to: settingsPath("organization"), icon: Building2 },
      { label: "Members", to: settingsPath("members"), icon: Users },
      { label: "Shared variables", to: settingsPath("variables"), icon: Braces },
      { label: "Notifications", to: settingsPath("notifications"), icon: BellRing },
    ],
  });
  const account: SettingsItem[] = [];
  if (hasPassword) account.push({ label: "Password", to: settingsPath("password"), icon: LockKeyhole });
  account.push({ label: "API tokens", to: settingsPath("tokens"), icon: KeyRound });
  sections.push({ title: "Account", items: account });
  return sections;
}
