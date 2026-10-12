import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import OrgAlertsPage from "@/pages/org-alerts";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers: [] }) }));

afterEach(() => vi.unstubAllGlobals());

describe("OrgAlertsPage", () => {
  it("opens on History and switches tabs", async () => {
    fakeAPI({
      "/api/v1/alerts/get-events": { events: [], next_cursor: "" },
      "/api/v1/alerts/get-rules": [],
      "/api/v1/alerts/get-channels": [],
    });
    const user = userEvent.setup({ delay: null });
    render(
      <MemoryRouter initialEntries={["/org/alerts"]}>
        <OrgAlertsPage />
      </MemoryRouter>,
    );
    expect(await screen.findByText(/No alerts yet/)).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Channels" }));
    expect(await screen.findByText("No channels yet")).toBeInTheDocument();
  });

  it("honours ?tab=", async () => {
    fakeAPI({ "/api/v1/alerts/get-rules": [], "/api/v1/alerts/get-channels": [] });
    render(
      <MemoryRouter initialEntries={["/org/alerts?tab=rules"]}>
        <OrgAlertsPage />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("button", { name: /Add rule/ })).toBeInTheDocument();
  });
});
