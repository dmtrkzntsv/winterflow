import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { stubViewport } from "@/test/media";

function renderDialog() {
  return render(
    <ResponsiveDialog
      open
      onOpenChange={() => {}}
      title="Add a domain"
      description="Covers its subdomains too."
      footer={<Button>Add</Button>}
    >
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
    expect(screen.getByRole("button", { name: "Add" })).toBeInTheDocument();
  });

  it("is a bottom sheet on a phone", () => {
    stubViewport(375);
    renderDialog();
    const sheet = screen.getByRole("dialog", { name: "Add a domain" });
    expect(sheet).toHaveClass("rounded-t-[10px]");
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
    // The footer stacks the primary (given last) on top, Cancel under it.
    expect(screen.getByRole("button", { name: "Add" }).parentElement).toHaveClass("flex-col-reverse");
  });
});
