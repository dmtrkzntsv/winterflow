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
    expect(alert).toHaveTextContent("Reloads stop at this app until it is fixed or stopped.");
    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("adds the reload hint to reload failures only", () => {
    renderStrip(rollout({ state: "failed", error: "pull access denied" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Upgrade to r3 stopped at bakomi");
    expect(screen.getByRole("alert")).not.toHaveTextContent("Reloads stop at this app");
  });

  it("reads sensibly when a failure has no app", () => {
    renderStrip(rollout({ kind: "vars_reload", state: "failed", error: "the rollout's scope is unreadable", current_app_id: "", current_server_id: "" }));
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Reload failed");
    expect(alert).toHaveTextContent("the rollout's scope is unreadable");
    expect(alert).not.toHaveTextContent("Reloads stop at this app");
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("tells what an Upgrade all skipped, and can be dismissed", async () => {
    renderStrip(rollout({ state: "done", done: 2, skipped: 1, revision: 5 }));
    expect(screen.getByRole("status")).toHaveTextContent("Upgraded 2 apps to r5 · 1 skipped: it needs a new value — upgrade it on its own.");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("tells what a stopped Upgrade all skipped", () => {
    renderStrip(rollout({ state: "stopped", done: 1, skipped: 2, revision: 5 }));
    expect(screen.getByRole("status")).toHaveTextContent(
      "Stopped after upgrading 1 app to r5 · 2 skipped: they need a new value — upgrade them on their own.",
    );
  });

  it("shows nothing for no rollout, or a finished or stopped one that skipped nothing or reloaded", () => {
    const quiet = { done: 3, skipped: 0 };
    const reload = { kind: "vars_reload" as const, skipped: 2 };
    for (const r of [null, rollout({ state: "done", ...quiet }), rollout({ state: "stopped", ...quiet }), rollout({ state: "done", ...reload })]) {
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
