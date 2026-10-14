import type { ReactNode } from "react";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerFooter,
  DrawerHeader,
  DrawerTitle,
} from "@/components/ui/drawer";
import { useMediaQuery } from "@/hooks/use-media-query";
import { cn } from "@/lib/utils";

// Phones (below 640px) get a bottom sheet instead of a centred dialog.
export const PHONE_QUERY = "(max-width: 639px)";

export type ResponsiveDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  // footer holds the actions (Cancel, then the primary).
  footer?: ReactNode;
  className?: string;
  // Focus hooks of the content: where focus goes when it opens (default:
  // its first field) and back to when it closes (default: what was focused
  // before it opened) — for a dialog opened from a row that may be gone.
  onOpenAutoFocus?: (event: Event) => void;
  onCloseAutoFocus?: (event: Event) => void;
};

// ResponsiveDialog is the standard dialog: shadcn's Dialog on wider
// screens, a vaul bottom sheet on phones, with the same title, body and
// footer.
export function ResponsiveDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  className,
  onOpenAutoFocus,
  onCloseAutoFocus,
}: ResponsiveDialogProps) {
  const phone = useMediaQuery(PHONE_QUERY);
  const describedBy = description ? {} : { "aria-describedby": undefined };

  if (phone) {
    return (
      <Drawer open={open} onOpenChange={onOpenChange} shouldScaleBackground={false}>
        <DrawerContent
          className={cn("max-h-[90svh]", className)}
          onOpenAutoFocus={onOpenAutoFocus}
          onCloseAutoFocus={onCloseAutoFocus}
          {...describedBy}
        >
          <DrawerHeader className="text-left">
            <DrawerTitle>{title}</DrawerTitle>
            {description ? <DrawerDescription>{description}</DrawerDescription> : null}
          </DrawerHeader>
          <div className="overflow-y-auto px-4 pb-2">{children}</div>
          {footer ? <DrawerFooter className="pb-[max(1rem,env(safe-area-inset-bottom))]">{footer}</DrawerFooter> : null}
        </DrawerContent>
      </Drawer>
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        // A long body scrolls between the header and the footer, which
        // stays in view.
        className={cn("flex max-h-[90svh] flex-col", className)}
        onOpenAutoFocus={onOpenAutoFocus}
        onCloseAutoFocus={onCloseAutoFocus}
        {...describedBy}
      >
        <DialogHeader className="pr-8">
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>
        {/* Padded out to the dialog's edge so focus rings aren't clipped. */}
        <div data-testid="dialog-body" className="-mx-6 -my-1 grid min-h-0 flex-1 content-start gap-4 overflow-y-auto px-6 py-1">
          {children}
        </div>
        {footer ? <DialogFooter className="shrink-0">{footer}</DialogFooter> : null}
      </DialogContent>
    </Dialog>
  );
}
