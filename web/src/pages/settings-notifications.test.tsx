import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import NotificationsSettingsPage from "@/pages/settings-notifications";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers: [] }) }));

afterEach(() => vi.unstubAllGlobals());

describe("NotificationsSettingsPage", () => {
  it("puts the channels and the alert rules on one page", async () => {
    fakeAPI({ "/api/v1/alerts/get-rules": [], "/api/v1/alerts/get-channels": [] });
    render(
      <MemoryRouter>
        <NotificationsSettingsPage />
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { level: 1, name: "Notifications" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Channels" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Alert rules" })).toBeInTheDocument();
    expect(await screen.findByText("No channels yet")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /Add rule/ })).toBeInTheDocument();
  });
});
