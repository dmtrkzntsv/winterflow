import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { CreateBar } from "@/components/new-app/create-bar";

const LONG = "Error response from daemon: failed to set up container networking: ac2c-a66267b3cac3-app-1 (01d029ee6bdf9f38c1e7d62ff263cc92f2e7c2e4770b)\nBind for 0.0.0.0:8088 failed: port is already allocated";

describe("CreateBar", () => {
  it("shows a failed create above the bar, wrapping, and moves focus to it", () => {
    render(
      <MemoryRouter>
        <CreateBar busy={false} onCreate={() => {}} cancelTo="/" error={LONG} />
      </MemoryRouter>,
    );
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("port is already allocated");
    const line = screen.getByText("Bind for 0.0.0.0:8088 failed: port is already allocated");
    expect(line.className).toContain("[overflow-wrap:anywhere]");
    expect(line.className).toContain("whitespace-pre-wrap");
    // The error comes before the bar, so the sticky bar never covers it.
    const box = alert.parentElement!;
    expect(document.activeElement).toBe(box);
    expect(box.compareDocumentPosition(screen.getByTestId("create-bar")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("has no error box without an error", () => {
    render(
      <MemoryRouter>
        <CreateBar busy onCreate={() => {}} cancelTo="/" />
      </MemoryRouter>,
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Creating…" })).toBeDisabled();
  });

  it("says a slow create is still running, links to Apps, and holds Create back for it", () => {
    render(
      <MemoryRouter>
        <CreateBar busy={false} onCreate={() => {}} cancelTo="/" stillCreatingTo="/server/s1/apps" held />
      </MemoryRouter>,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Still creating — this can take a few minutes. It will appear in Apps.");
    expect(screen.getByRole("link", { name: "Apps" })).toHaveAttribute("href", "/server/s1/apps");
    expect(screen.getByRole("button", { name: "Creating…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled();
  });
});
