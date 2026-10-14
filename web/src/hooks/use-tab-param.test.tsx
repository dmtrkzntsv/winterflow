import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { useTabParam } from "@/hooks/use-tab-param";

const TABS = ["overview", "logs", "configuration"] as const;

function Probe() {
  const [tab, setTab] = useTabParam(TABS, "overview", { aliases: { editor: "configuration" } });
  const { search } = useLocation();
  return (
    <div>
      <span data-testid="tab">{tab}</span>
      <span data-testid="search">{search}</span>
      <button onClick={() => setTab("logs")}>logs</button>
      <button onClick={() => setTab("overview")}>overview</button>
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

describe("useTabParam", () => {
  it("falls back without a tab or with one it doesn't know", () => {
    renderAt("/app");
    expect(screen.getByTestId("tab")).toHaveTextContent("overview");
  });

  it("reads a known tab", () => {
    renderAt("/app?tab=logs");
    expect(screen.getByTestId("tab")).toHaveTextContent("logs");
  });

  it("ignores an unknown tab", () => {
    renderAt("/app?tab=nope");
    expect(screen.getByTestId("tab")).toHaveTextContent("overview");
  });

  it("does not take an Object.prototype name for an alias", () => {
    renderAt("/app?tab=toString");
    expect(screen.getByTestId("tab")).toHaveTextContent("overview");
    expect(screen.getByTestId("search")).toHaveTextContent("?tab=toString");
  });

  it("opens an old tab name under its new name and rewrites the URL", async () => {
    renderAt("/app?tab=editor&x=1");
    expect(screen.getByTestId("tab")).toHaveTextContent("configuration");
    expect(await screen.findByText("?tab=configuration&x=1")).toBeInTheDocument();
  });

  it("keeps other parameters and leaves the default tab out of the URL", async () => {
    const user = userEvent.setup();
    renderAt("/app?x=1");
    await user.click(screen.getByRole("button", { name: "logs" }));
    expect(screen.getByTestId("search")).toHaveTextContent("?x=1&tab=logs");
    await user.click(screen.getByRole("button", { name: "overview" }));
    expect(screen.getByTestId("search")).toHaveTextContent(/^\?x=1$/);
  });
});
