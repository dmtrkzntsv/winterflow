import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AlertDialog, AlertDialogContent, AlertDialogDescription, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from "@/components/ui/drawer";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet";

const overlays = () => [...document.body.querySelectorAll(".wf-overlay")];

describe("overlays", () => {
  it("share one light overlay, a confirm's portaled after the sheet under it", () => {
    render(
      <>
        <Sheet open>
          <SheetContent>
            <SheetTitle>History</SheetTitle>
            <SheetDescription>versions</SheetDescription>
          </SheetContent>
        </Sheet>
        <AlertDialog open>
          <AlertDialogContent>
            <AlertDialogTitle>Roll back?</AlertDialogTitle>
            <AlertDialogDescription>sure</AlertDialogDescription>
          </AlertDialogContent>
        </AlertDialog>
      </>,
    );
    const [under, top] = overlays();
    expect(overlays()).toHaveLength(2);
    // Siblings in <body>, the top one last: the CSS clears every overlay
    // that has a later one beside it.
    expect(under.parentElement).toBe(top.parentElement);
    expect(under.compareDocumentPosition(top) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    for (const o of overlays()) expect(o.className).not.toMatch(/bg-black/);
  });

  it("puts the shared overlay under a dialog (the palette's too)", () => {
    render(
      <Dialog open>
        <DialogContent>
          <DialogTitle>New app</DialogTitle>
          <DialogDescription>How?</DialogDescription>
        </DialogContent>
      </Dialog>,
    );
    expect(overlays()).toHaveLength(1);
    expect(overlays()[0]).toHaveClass("fixed", "inset-0", "z-50");
  });

  it("marks every overlay open or closed, the drawer's too, for the CSS that clears the one under an open one", () => {
    render(
      <>
        <Drawer open>
          <DrawerContent>
            <DrawerTitle>New app</DrawerTitle>
            <DrawerDescription>How?</DrawerDescription>
          </DrawerContent>
        </Drawer>
        <AlertDialog open>
          <AlertDialogContent>
            <AlertDialogTitle>Sure?</AlertDialogTitle>
            <AlertDialogDescription>sure</AlertDialogDescription>
          </AlertDialogContent>
        </AlertDialog>
      </>,
    );
    expect(overlays()).toHaveLength(2);
    // index.css clears an overlay only under an open one
    // (.wf-overlay[data-state="open"]): one closing gives the tint back.
    for (const o of overlays()) expect(o).toHaveAttribute("data-state", "open");
  });
});
