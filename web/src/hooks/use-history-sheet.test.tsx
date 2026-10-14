import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { useHistorySheet } from "@/hooks/use-history-sheet";
import { useTabParam } from "@/hooks/use-tab-param";
import { LEGACY_APP_TABS } from "@/lib/routes";

const TABS = ["overview", "logs", "configuration", "backups", "metrics", "settings"] as const;

// Probe uses the two hooks in the page's order.
function Probe() {
  const [tab] = useTabParam(TABS, "overview", { aliases: LEGACY_APP_TABS });
  const [open, setOpen] = useHistorySheet();
  const { search } = useLocation();
  return (
    <div>
      <span data-testid="tab">{tab}</span>
      <span data-testid="open">{String(open)}</span>
      <span data-testid="search">{search}</span>
      <button onClick={() => setOpen(true)}>open</button>
      <button onClick={() => setOpen(false)}>close</button>
    </div>
  );
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Probe />
    </MemoryRouter>,
  );
}

describe("useHistorySheet", () => {
  it("opens Configuration with the sheet from an old ?tab=history link, rewriting it once", async () => {
    renderAt("/app?tab=history&x=1");
    expect(screen.getByTestId("tab")).toHaveTextContent("configuration");
    expect(screen.getByTestId("open")).toHaveTextContent("true");
    expect(await screen.findByText("?tab=configuration&x=1&history=1")).toBeInTheDocument();
    expect(screen.getByTestId("open")).toHaveTextContent("true");
  });

  it("keeps the sheet in ?history=1 and drops it on close", async () => {
    const user = userEvent.setup();
    renderAt("/app?tab=logs");
    expect(screen.getByTestId("open")).toHaveTextContent("false");
    await user.click(screen.getByRole("button", { name: "open" }));
    expect(screen.getByTestId("search")).toHaveTextContent("?tab=logs&history=1");
    expect(screen.getByTestId("open")).toHaveTextContent("true");
    await user.click(screen.getByRole("button", { name: "close" }));
    expect(screen.getByTestId("search")).toHaveTextContent(/^\?tab=logs$/);
  });

  it("closing a sheet opened by an old link leaves Configuration without it", async () => {
    const user = userEvent.setup();
    renderAt("/app?tab=history");
    await user.click(screen.getByRole("button", { name: "close" }));
    expect(screen.getByTestId("search")).toHaveTextContent(/^\?tab=configuration$/);
    expect(screen.getByTestId("open")).toHaveTextContent("false");
  });
});
