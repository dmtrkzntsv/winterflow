import { ChevronsUpDown, LogOut } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar";
import { useSidebar } from "@/components/ui/use-sidebar";
import { useAuth } from "@/context/use-auth";
import { useUser } from "@/context/use-user";
import { useThemePreference } from "@/hooks/use-theme";
import { initials } from "@/lib/members";
import { THEME_OPTIONS } from "@/lib/theme-options";
import type { ThemePreference } from "@/lib/theme";

// NavUser is the account footer of the sidebar: who is signed in, the
// theme, Sign out. The organization's and the account's pages are in
// Settings.
export function NavUser() {
  const { isMobile } = useSidebar();
  const { logout } = useAuth();
  const { user, loading } = useUser();
  const [theme, setTheme] = useThemePreference();
  const { t } = useTranslation();
  const displayName = user?.name ?? (loading ? "Loading..." : "Unknown user");
  // The same initials as the member list (one helper for every avatar).
  const avatarFallback = user?.name?.trim() ? initials(user.name, "") : "WF";

  const identity = (
    <>
      <Avatar className="h-8 w-8 rounded-lg">
        <AvatarImage src={user?.picture} alt={displayName} />
        <AvatarFallback className="rounded-lg">{avatarFallback}</AvatarFallback>
      </Avatar>
      <div className="grid flex-1 text-left text-sm leading-tight">
        <span className="truncate font-medium">{displayName}</span>
      </div>
    </>
  );

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              {identity}
              <ChevronsUpDown className="ml-auto size-4" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            className="w-(--radix-dropdown-menu-trigger-width) min-w-56 rounded-lg"
            side={isMobile ? "bottom" : "right"}
            align="end"
            sideOffset={4}
          >
            <DropdownMenuLabel className="p-0 font-normal">
              <div className="flex items-center gap-2 px-1 py-1.5 text-left text-sm">{identity}</div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">Theme</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as ThemePreference)}>
              {THEME_OPTIONS.map((item) => (
                <DropdownMenuRadioItem
                  key={item.value}
                  value={item.value}
                  aria-label={item.label}
                  className="min-h-10 gap-2"
                >
                  <item.icon className="size-4" aria-hidden />
                  {item.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="min-h-10" onClick={() => logout()}>
              <LogOut />
              {t("auth.logout")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
