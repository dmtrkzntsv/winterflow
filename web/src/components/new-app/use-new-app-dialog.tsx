import { useState, type ReactNode } from "react";

import { NewAppChoices } from "@/components/new-app/new-app-choices";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { useTemplateCount } from "@/hooks/use-template-count";

// NEW_APP_TITLE and NEW_APP_ASK head the ways to start, in the dialog and
// on the /new page alike.
export const NEW_APP_TITLE = "New app";
export const NEW_APP_ASK = "How do you want to start?";

// useNewAppDialog is the New app chooser every button opens (a dialog; a
// bottom sheet on phones): open() shows it; dialog is the element to
// render. The organization's templates are counted on the first open and
// kept for the opener's life.
export function useNewAppDialog(serverId: string): { open: () => void; dialog: ReactNode } {
  const [shown, setShown] = useState(false);
  const [opened, setOpened] = useState(false);
  const templates = useTemplateCount(opened);
  return {
    open: () => {
      setOpened(true);
      setShown(true);
    },
    dialog: (
      <ResponsiveDialog
        open={shown}
        onOpenChange={setShown}
        title={NEW_APP_TITLE}
        description={NEW_APP_ASK}
        className="sm:max-w-2xl"
      >
        {shown ? <NewAppChoices serverId={serverId} templates={templates} onPick={() => setShown(false)} /> : null}
      </ResponsiveDialog>
    ),
  };
}
