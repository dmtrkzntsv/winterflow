import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { LogsView, type LogLine } from "@/components/logs-view";

const line = (container: string | undefined, message: string): LogLine => ({ timestamp: 1_760_000_000, level: 0, message, container });

function renderLogs(lines: LogLine[], over: Partial<Parameters<typeof LogsView>[0]> = {}) {
  const props = { lines, loading: false, error: null, tail: 200, onTailChange: vi.fn(), onRefresh: vi.fn(), ...over };
  render(<LogsView {...props} />);
  return props;
}

describe("LogsView", () => {
  it("filters by container with chips when there are several", async () => {
    const user = userEvent.setup();
    renderLogs([line("web", "GET /"), line("db", "checkpoint"), line("web", "GET /health")]);
    const chips = screen.getByRole("group", { name: "Containers" });
    expect(within(chips).getAllByRole("button").map((b) => b.textContent)).toEqual(["All", "web", "db"]);
    expect(within(chips).getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("3 lines")).toBeInTheDocument();

    await user.click(within(chips).getByRole("button", { name: "web" }));
    const log = screen.getByRole("log", { name: "Logs" });
    expect(log).toHaveTextContent("GET /health");
    expect(log).not.toHaveTextContent("checkpoint");
    expect(within(chips).getByRole("button", { name: "web" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("2 lines")).toBeInTheDocument();

    await user.click(within(chips).getByRole("button", { name: "All" }));
    expect(screen.getByRole("log", { name: "Logs" })).toHaveTextContent("checkpoint");
  });

  it("has no chips with one container, only its name", () => {
    renderLogs([line("web", "GET /")]);
    expect(screen.queryByRole("group", { name: "Containers" })).not.toBeInTheDocument();
    expect(screen.getByText("1 line")).toBeInTheDocument();
    expect(screen.getAllByText("web").length).toBeGreaterThan(0);
  });

  it("refreshes and says when there is no output", async () => {
    const user = userEvent.setup();
    const props = renderLogs([]);
    expect(screen.getByText("No output yet. Start the app, then refresh.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Refresh logs" }));
    expect(props.onRefresh).toHaveBeenCalled();
  });

  it("offers Jump to latest once scrolled up, and sticks to the bottom again", async () => {
    const user = userEvent.setup();
    renderLogs([line("web", "a"), line("web", "b")]);
    const log = screen.getByRole("log", { name: "Logs" });
    Object.defineProperty(log, "scrollHeight", { configurable: true, value: 1000 });
    Object.defineProperty(log, "clientHeight", { configurable: true, value: 100 });
    log.scrollTop = 0;
    fireEvent.scroll(log);
    await user.click(await screen.findByRole("button", { name: "Jump to latest" }));
    expect(log.scrollTop).toBe(1000);
    expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument();
  });

  it("shows a read failure with Try again", async () => {
    const user = userEvent.setup();
    const props = renderLogs([], { error: "agent offline" });
    expect(screen.getByText("agent offline")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(props.onRefresh).toHaveBeenCalled();
  });
});
