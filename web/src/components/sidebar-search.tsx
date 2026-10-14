import { Search } from "lucide-react";

import { Kbd } from "@/components/ui/kbd";
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar";
import { useSidebar } from "@/components/ui/use-sidebar";
import { useCommandPalette } from "@/context/use-palette";
import { shortcutLabel } from "@/lib/keyboard";

// SidebarSearch is the sidebar's search box: it opens the ⌘K palette (and
// closes the phone sheet first).
export function SidebarSearch() {
  const { openPalette } = useCommandPalette();
  const { isMobile, setOpenMobile } = useSidebar();
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <SidebarMenuButton
          tooltip="Search"
          aria-keyshortcuts="Meta+K Control+K"
          className="border border-sidebar-border bg-background/60 text-muted-foreground max-lg:h-10"
          onClick={() => {
            if (isMobile) setOpenMobile(false);
            openPalette();
          }}
        >
          <Search />
          <span>Search…</span>
          <Kbd className="ml-auto group-data-[collapsible=icon]:hidden max-sm:hidden pointer-coarse:hidden">{shortcutLabel("K")}</Kbd>
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
