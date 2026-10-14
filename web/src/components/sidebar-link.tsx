import type { ComponentProps } from "react";
import { Link } from "react-router-dom";

import { useSidebar } from "@/components/ui/use-sidebar";

// SidebarLink is a router link that closes the phone sidebar sheet once it
// is followed (the sheet would otherwise stay over the new page).
export function SidebarLink({ onClick, ...props }: ComponentProps<typeof Link>) {
  const { isMobile, setOpenMobile } = useSidebar();
  return (
    <Link
      {...props}
      onClick={(event) => {
        onClick?.(event);
        if (isMobile) setOpenMobile(false);
      }}
    />
  );
}
