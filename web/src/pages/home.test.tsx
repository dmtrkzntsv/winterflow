import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import HomePage from "@/pages/home";

vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: vi.fn() }));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ refresh: vi.fn() }) }));
vi.mock("@/components/server-cards", () => ({ ServerCards: () => <div data-testid="strip" /> }));
vi.mock("@/components/app-grid", () => ({ AppGrid: () => <div data-testid="grid" /> }));
vi.mock("@/components/server-creation-dialog", () => ({ ServerSelectionDialog: () => null }));

describe("HomePage", () => {
  it("is the apps page: title with New App, server strip above the grid", () => {
    render(
      <MemoryRouter>
        <HomePage />
      </MemoryRouter>,
    );
    const title = screen.getByRole("heading", { level: 1, name: "Apps" });
    expect(title.parentElement).toContainElement(screen.getByRole("button", { name: /New App/ }));
    const strip = screen.getByTestId("strip");
    const grid = screen.getByTestId("grid");
    expect(strip.compareDocumentPosition(grid) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
