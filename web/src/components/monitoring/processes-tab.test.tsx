import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ProcessesTab } from "@/components/monitoring/processes-tab";
import type { ProcessInfo, ProcessList } from "@/lib/processes";

const useProcesses = vi.fn();
let result: { data: ProcessList | null; error: string | null; updatedAt: number | null; refresh: () => void };
vi.mock("@/hooks/use-processes", () => ({
  useProcesses: (...a: unknown[]) => {
    useProcesses(...a);
    return result;
  },
}));
let visible = true;
vi.mock("@/hooks/use-page-visible", () => ({ usePageVisible: () => visible }));
vi.mock("@/context/use-apps", () => ({ useApps: () => ({ apps: [{ id: "app-1", name: "Nextcloud" }] }) }));

const p = (o: Partial<ProcessInfo>): ProcessInfo => ({
  pid: 1, ppid: 0, name: "x", cmdline: "", user: "root", state: "S", threads: 1, start_time: 0,
  kernel: false, cpu_pct: 0, rss: 0, mem_pct: 0, read_bps: 0, write_bps: 0, ...o,
});

const LIST: ProcessList = {
  sampled_at: 0, ncpu: 4, mem_total: 1 << 30, total: 4,
  processes: [
    p({ pid: 10, name: "postgres", cpu_pct: 5, app_id: "app-1", write_bps: null, read_bps: null }),
    p({ pid: 7, name: "sshd", cpu_pct: 20 }),
    p({ pid: 2, name: "kthreadd", kernel: true }),
    p({ pid: 8, name: "bash", cpu_pct: 1 }),
  ],
};

function names() {
  return within(screen.getByRole("table")).getAllByTestId("process-name").map((n) => n.textContent);
}

function renderTab() {
  return render(<MemoryRouter><ProcessesTab serverId="s1" /></MemoryRouter>);
}

describe("ProcessesTab", () => {
  beforeEach(() => {
    useProcesses.mockReset();
    visible = true;
    result = { data: LIST, error: null, updatedAt: Date.now(), refresh: vi.fn() };
  });

  it("polls only while the page is visible and not paused", async () => {
    const user = userEvent.setup();
    const { rerender } = renderTab();
    expect(useProcesses).toHaveBeenLastCalledWith("s1", true);
    await user.click(screen.getByRole("button", { name: "Pause" }));
    expect(useProcesses).toHaveBeenLastCalledWith("s1", false);
    await user.click(screen.getByRole("button", { name: "Resume" }));
    visible = false;
    rerender(<MemoryRouter><ProcessesTab serverId="s1" /></MemoryRouter>);
    expect(useProcesses).toHaveBeenLastCalledWith("s1", false);
  });

  it("sorts by CPU, highest first, and hides kernel threads by default", () => {
    renderTab();
    expect(names()).toEqual(["sshd", "postgres", "bash"]);
    expect(screen.getByText(/4 cores/)).toBeInTheDocument();
  });

  it("re-sorts on a header click and flips on a second click", async () => {
    const user = userEvent.setup();
    renderTab();
    await user.click(screen.getByRole("button", { name: "Sort by Process" }));
    expect(names()).toEqual(["bash", "postgres", "sshd"]);
    await user.click(screen.getByRole("button", { name: "Sort by Process" }));
    expect(names()).toEqual(["sshd", "postgres", "bash"]);
  });

  it("keeps the chosen sort across a refresh", async () => {
    const user = userEvent.setup();
    const { rerender } = renderTab();
    await user.click(screen.getByRole("button", { name: "Sort by Process" }));
    result = { ...result, data: { ...LIST, processes: [...LIST.processes, p({ pid: 3, name: "aaa", cpu_pct: 90 })] } };
    rerender(<MemoryRouter><ProcessesTab serverId="s1" /></MemoryRouter>);
    expect(names()[0]).toBe("aaa");
  });

  it("filters by text, by app, and shows kernel threads on request", async () => {
    const user = userEvent.setup();
    renderTab();
    await user.type(screen.getByRole("searchbox", { name: "Filter processes" }), "nextcloud");
    expect(names()).toEqual(["postgres"]);
    await user.clear(screen.getByRole("searchbox", { name: "Filter processes" }));
    await user.click(screen.getByRole("switch", { name: "Show kernel threads" }));
    expect(names()).toContain("kthreadd");
    await user.type(screen.getByRole("searchbox", { name: "Filter processes" }), "zzz");
    expect(screen.getByText("No processes match.")).toBeInTheDocument();
  });

  it("renders a dash for unknown rates and links the app", () => {
    renderTab();
    const row = screen.getAllByTestId("process-row").find((r) => within(r).queryByText("postgres"))!;
    expect(within(row).getAllByText("—").length).toBeGreaterThanOrEqual(2);
    expect(within(row).getByRole("link", { name: "Nextcloud" })).toHaveAttribute("href", "/server/s1/app/app-1");
  });

  it("shows skeleton rows before the first answer and an inline error after", () => {
    result = { data: null, error: null, updatedAt: null, refresh: vi.fn() };
    const { rerender } = renderTab();
    expect(screen.getByTestId("processes-loading")).toBeInTheDocument();
    result = { data: LIST, error: "The server did not answer in time.", updatedAt: Date.now(), refresh: vi.fn() };
    rerender(<MemoryRouter><ProcessesTab serverId="s1" /></MemoryRouter>);
    expect(screen.getByText("The server did not answer in time.")).toBeInTheDocument();
    expect(names()).toHaveLength(3); // the last good list stays
  });

  it("says when the list is cut", () => {
    result = { ...result, data: { ...LIST, total: 2500 } };
    renderTab();
    expect(screen.getByText(/busiest 4 of 2500/)).toBeInTheDocument();
  });
});
