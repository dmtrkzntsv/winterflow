import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

const toast = vi.hoisted(() => ({ error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

import { RolloutStrip } from "@/components/rollout-strip";
import type { Rollout } from "@/lib/rollouts";
import { rollout } from "@/test/rollouts";

const names = (id: string) => (id === "s1" ? "alpha" : id);

function renderStrip(r: Rollout | null, onStop = vi.fn(async () => {})) {
  render(
    <MemoryRouter>
      <RolloutStrip rollout={r} serverName={names} onStop={onStop} />
    </MemoryRouter>,
  );
  return onStop;
}

describe("RolloutStrip", () => {
  it("shows the app in flight and stops after it", async () => {
    const onStop = renderStrip(rollout());
    expect(screen.getByRole("status")).toHaveTextContent("Upgrading 3 of 12: bakomi…");
    await userEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(onStop).toHaveBeenCalledWith("r1");
  });

  it("disables Stop once asked", () => {
    renderStrip(rollout({ stop_requested: true }));
    expect(screen.getByRole("button", { name: "Stop" })).toBeDisabled();
  });

  it("toasts a refused stop", async () => {
    renderStrip(rollout(), vi.fn(async () => Promise.reject(new Error("rollout not found"))));
    await userEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(toast.error).toHaveBeenCalledWith("Couldn't stop it", { description: "rollout not found" });
  });

  it("names the app a failure stopped at, links to it, and can be dismissed", async () => {
    renderStrip(rollout({ kind: "vars_reload", state: "failed", error: "docker compose up: exit status 1", current_app_name: "shop" }));
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Reload stopped at shop on alpha");
    expect(alert).toHaveTextContent("docker compose up: exit status 1");
    expect(screen.getByRole("link", { name: "Open shop" })).toHaveAttribute("href", "/server/s1/app/a1b2c3d4e5f6");
    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows nothing for no rollout, a finished one or a stopped one", () => {
    for (const r of [null, rollout({ state: "done" }), rollout({ state: "stopped" })]) {
      const { container, unmount } = render(
        <MemoryRouter>
          <RolloutStrip rollout={r} serverName={names} onStop={vi.fn()} />
        </MemoryRouter>,
      );
      expect(container).toBeEmptyDOMElement();
      unmount();
    }
  });
});
