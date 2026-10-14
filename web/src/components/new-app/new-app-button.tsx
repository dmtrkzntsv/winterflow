import { Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useNewAppDialog } from "@/components/new-app/use-new-app-dialog";

// NewAppButton is the Apps page's primary action: it opens the ways to
// start, each leading to its form.
export function NewAppButton({ serverId, className }: { serverId: string; className?: string }) {
  const chooser = useNewAppDialog(serverId);
  return (
    <>
      <Button className={className} onClick={chooser.open}>
        <Plus className="size-4" /> New app
      </Button>
      {chooser.dialog}
    </>
  );
}
