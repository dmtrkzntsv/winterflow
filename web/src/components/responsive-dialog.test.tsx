import type { ReactNode } from "react";
import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { stubViewport } from "@/test/media";

function renderDialog(
  footer: ReactNode = (
    <>
      <Button variant="outline">Cancel</Button>
      <Button>Add</Button>
    </>
  ),
) {
  return render(
    <ResponsiveDialog open onOpenChange={() => {}} title="Add a domain" description="Covers its subdomains too." footer={footer}>
      <label>
        Name <input />
      </label>
    </ResponsiveDialog>,
  );
}

describe("ResponsiveDialog", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("is a centred dialog on a wider screen", () => {
    renderDialog();
    const dialog = screen.getByRole("dialog", { name: "Add a domain" });
    expect(dialog).toHaveClass("sm:rounded-lg");
    expect(dialog).toHaveTextContent("Covers its subdomains too.");
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
    // Cancel, then the primary, as given.
    expect(within(dialog).getAllByRole("button", { name: /Cancel|Add/ }).map((b) => b.textContent)).toEqual(["Cancel", "Add"]);
  });

  it("is a bottom sheet on a phone", () => {
    stubViewport(375);
    renderDialog();
    const sheet = screen.getByRole("dialog", { name: "Add a domain" });
    expect(sheet).toHaveClass("rounded-t-[10px]");
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
    // The primary (given last) comes first, on top and in focus order;
    // Cancel last. Nothing reverses it visually.
    expect(within(sheet).getAllByRole("button", { name: /Cancel|Add/ }).map((b) => b.textContent)).toEqual(["Add", "Cancel"]);
    expect(screen.getByRole("button", { name: "Add" }).parentElement).not.toHaveClass("flex-col-reverse");
  });

  it("puts an extra action first on a wider screen, and just above Cancel on a phone", () => {
    const aside = <Button variant="ghost">Reset</Button>;
    const names = () => screen.getAllByRole("button", { name: /Cancel|Add|Reset/ }).map((b) => b.textContent);
    const footer = (
      <>
        <Button variant="outline">Cancel</Button>
        <Button>Add</Button>
      </>
    );
    const wide = render(
      <ResponsiveDialog open onOpenChange={() => {}} title="Adjust" footer={footer} aside={aside}>
        body
      </ResponsiveDialog>,
    );
    expect(names()).toEqual(["Reset", "Cancel", "Add"]);
    wide.unmount();
    stubViewport(375);
    render(
      <ResponsiveDialog open onOpenChange={() => {}} title="Adjust" footer={footer} aside={aside}>
        body
      </ResponsiveDialog>,
    );
    expect(names()).toEqual(["Add", "Reset", "Cancel"]);
  });

  it("keeps a footer that isn't a fragment as given on a phone", () => {
    stubViewport(375);
    render(
      <ResponsiveDialog open onOpenChange={() => {}} title="Done" footer={<Button>Done</Button>} aside={<Button>Reset</Button>}>
        body
      </ResponsiveDialog>,
    );
    expect(screen.getAllByRole("button", { name: /Done|Reset/ }).map((b) => b.textContent)).toEqual(["Reset", "Done"]);
  });
});
