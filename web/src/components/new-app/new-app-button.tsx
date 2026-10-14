import { useState } from "react";
import { Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { NewAppChoices } from "@/components/new-app/new-app-choices";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { useTemplateCount } from "@/hooks/use-template-count";

// NewAppButton is the Apps page's primary action: it opens the ways to
// start (a dialog; a bottom sheet on phones), each leading to its form.
export function NewAppButton({ serverId, className }: { serverId: string; className?: string }) {
  const [open, setOpen] = useState(false);
  // Read on the first open and kept for the button's life.
  const [opened, setOpened] = useState(false);
  const templates = useTemplateCount(opened);
  return (
    <>
      <Button className={className} onClick={() => {
          setOpened(true);
          setOpen(true);
        }}>
        <Plus className="size-4" /> New app
      </Button>
      <ResponsiveDialog open={open} onOpenChange={setOpen} title="New app" description="How do you want to start?">
        {open ? <NewAppChoices serverId={serverId} templates={templates} onPick={() => setOpen(false)} /> : null}
      </ResponsiveDialog>
    </>
  );
}
