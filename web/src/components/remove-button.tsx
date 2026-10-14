import { Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";

// RemoveButton is a row's only action when that action is a removal: a
// trash icon named for what it removes ("Remove registry ghcr.io"). It
// opens a ConfirmDialog; it never removes by itself.
export function RemoveButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Button
      size="icon"
      variant="ghost"
      className="size-10 text-muted-foreground hover:text-destructive sm:size-8"
      aria-label={label}
      title={label}
      onClick={onClick}
    >
      <Trash2 className="size-4" />
    </Button>
  );
}
