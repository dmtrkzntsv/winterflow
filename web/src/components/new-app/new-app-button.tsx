import { useState } from "react";
import { Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { NewAppChoices } from "@/components/new-app/new-app-choices";
import { ResponsiveDialog } from "@/components/responsive-dialog";

// NewAppButton is the Apps page's primary action: it opens the ways to
// start (a dialog; a bottom sheet on phones), each leading to its form.
export function NewAppButton({ serverId, className }: { serverId: string; className?: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button className={className} onClick={() => setOpen(true)}>
        <Plus className="size-4" /> New app
      </Button>
      <ResponsiveDialog open={open} onOpenChange={setOpen} title="New app" description="How do you want to start?">
        {open ? <NewAppChoices serverId={serverId} onPick={() => setOpen(false)} /> : null}
      </ResponsiveDialog>
    </>
  );
}
