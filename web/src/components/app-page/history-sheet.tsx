import { HistoryPanel } from "@/components/app-page/history-panel";
import type { Ownership } from "@/components/app-page/ownership";
import { Button } from "@/components/ui/button";
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet";

// HistorySheet is the app's history in a side sheet (full width on a
// phone), opened from Configuration's bar or the Overview. Nothing is read
// while it is closed.
export function HistorySheet({
  appId,
  ownership,
  open,
  onOpenChange,
}: {
  appId: string;
  ownership: Ownership;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-md">
        <SheetHeader className="border-b px-5 py-4 text-left">
          <SheetTitle>History</SheetTitle>
          <SheetDescription>Every saved change, newest first.</SheetDescription>
        </SheetHeader>
        <div className="flex-1 overflow-y-auto px-5 py-4">{open ? <HistoryPanel appId={appId} ownership={ownership} /> : null}</div>
        {/* A text Close for phones, where the corner X is easy to miss. */}
        <SheetFooter className="border-t px-5 py-3 sm:hidden">
          <SheetClose asChild>
            <Button variant="outline" className="w-full">
              Close
            </Button>
          </SheetClose>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}
