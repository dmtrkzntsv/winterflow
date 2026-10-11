import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { AppGrid } from "@/components/app-grid";
import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";

function renderGrid(over: Partial<AppsContextValue>) {
  const value = {
    apps: [
      { id: "a1", name: "Shop", icon: "", color: "" },
      { id: "a2", name: "Blog", icon: "", color: "" },
    ],
    statusByApp: { a1: 1, a2: 1 },
    backupsByApp: {},
    loading: false,
    error: null,
    ...over,
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={value}>
        <AppGrid />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

describe("AppGrid", () => {
  it("puts a warning dot on apps with a late or failing backup", () => {
    renderGrid({ backupsByApp: { a1: [{ name: "db", state: "failing", last_error: "disk full" }], a2: [{ name: "db", state: "ok" }] } });
    expect(screen.getAllByRole("img", { name: "A backup is late or failing" })).toHaveLength(1);
    expect(screen.getByRole("link", { name: /Shop/ })).toHaveAttribute("title", "Shop: Running (a backup is late or failing)");
    expect(screen.getByRole("link", { name: /Blog/ })).toHaveAttribute("title", "Blog: Running");
  });

  it("shows no dot without backups", () => {
    renderGrid({});
    expect(screen.queryByRole("img", { name: "A backup is late or failing" })).not.toBeInTheDocument();
  });
});
