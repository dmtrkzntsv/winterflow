import { Link, Navigate, NavLink, Outlet, useLocation } from "react-router-dom";
import { ChevronLeft, ChevronRight } from "lucide-react";

import { PageHeader } from "@/components/page-header";
import { useProfile } from "@/context/use-profile";
import { useServers } from "@/context/use-servers";
import { useIsMobile } from "@/hooks/use-mobile";
import { useSettingsSections } from "@/hooks/use-settings-sections";
import { SETTINGS_PATH, serverSettingsPath, settingsPath } from "@/lib/routes";
import type { SettingsSection } from "@/lib/settings-nav";
import { cn } from "@/lib/utils";

function SettingsMenu({ sections, large = false }: { sections: SettingsSection[]; large?: boolean }) {
  return (
    <nav aria-label="Settings" className="space-y-6">
      {sections.map((section) => (
        <div key={section.title} className="space-y-1">
          <h2 className="truncate px-2 text-xs font-medium text-muted-foreground">{section.title}</h2>
          <ul className="space-y-0.5">
            {section.items.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  className={({ isActive }) =>
                    cn(
                      "flex items-center gap-2 rounded-md px-2 text-sm transition-colors duration-150 hover:bg-accent hover:text-accent-foreground",
                      large ? "min-h-11" : "min-h-8",
                      isActive && "bg-accent font-medium text-accent-foreground",
                    )
                  }
                >
                  <item.icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                  <span className="flex-1 truncate">{item.label}</span>
                  {large ? <ChevronRight className="size-4 text-muted-foreground" aria-hidden /> : null}
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

// SettingsLayout is the one Settings area (spec §6): the sub-menu in a left
// column beside the page on a desktop; on a phone /settings is the menu as
// a list and every sub-page shows alone with a way back to it.
export function SettingsLayout() {
  const sections = useSettingsSections();
  const isMobile = useIsMobile();
  const { profile } = useProfile();
  const { pathname } = useLocation();
  const atIndex = pathname.replace(/\/+$/, "") === SETTINGS_PATH;

  // A user on a temporary password may only set a new one: every other
  // Settings page bounces back to Password, so the menu and the way back to
  // it are left out.
  if (profile?.must_change_password) return <Outlet />;

  if (isMobile) {
    if (atIndex) {
      return (
        <div className="space-y-6">
          <PageHeader title="Settings" />
          <SettingsMenu sections={sections} large />
        </div>
      );
    }
    return (
      <div className="space-y-4">
        <Link
          to={SETTINGS_PATH}
          className="inline-flex min-h-10 items-center gap-1 text-sm text-muted-foreground transition-colors duration-150 hover:text-foreground"
        >
          <ChevronLeft className="size-4" aria-hidden />
          Settings
        </Link>
        <Outlet />
      </div>
    );
  }

  return (
    <div className="flex gap-8">
      <aside className="w-52 shrink-0">
        <SettingsMenu sections={sections} />
      </aside>
      <div className="min-w-0 flex-1">
        <Outlet />
      </div>
    </div>
  );
}

// SettingsIndex is /settings on a desktop: the active server's General
// page, or the organization's when there is no server yet.
export function SettingsIndex() {
  const { activeServerId, servers, loading } = useServers();
  if (loading && servers.length === 0) return null;
  return (
    <Navigate replace to={activeServerId ? serverSettingsPath(activeServerId) : settingsPath("organization")} />
  );
}
