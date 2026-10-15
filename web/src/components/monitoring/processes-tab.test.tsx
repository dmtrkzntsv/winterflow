import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ProcessesTab } from "@/components/monitoring/processes-tab";
import type { ProcessGroup, ProcessInfo, ProcessList } from "@/lib/processes";

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
  kernel: false, cpu_pct: 0, rss: 0, mem_pct: 0, ...o,
});

const g = (o: Partial<ProcessGroup>): ProcessGroup => ({
  id: "g", label: "", kind: "service", processes: 1, read_bps: null, write_bps: null, ...o,
});

const LIST: ProcessList = {
  sampled_at: 0, ncpu: 4, mem_total: 1 << 30, total: 4,
  processes: [
    p({ pid: 10, name: "postgres", cpu_pct: 5, app_id: "app-1", group: "c1" }),
    p({ pid: 7, name: "sshd", cpu_pct: 20, group: "ssh" }),
    p({ pid: 2, name: "kthreadd", kernel: true }),
    p({ pid: 8, name: "bash", cpu_pct: 1, group: "c2" }),
  ],
  groups: [
    g({ id: "c1", kind: "container", container_id: "aaaaaaaaaaaaaaaa", app_id: "app-1", processes: 3, read_bps: 1024, write_bps: 2048 }),
    g({ id: "ssh", label: "ssh.service", processes: 1, read_bps: 0, write_bps: 512 }),
    g({ id: "c2", kind: "container", container_id: "0123456789abcdef", processes: 2 }),
  ],
};

function groupList() {
  return screen.getByRole("list", { name: "Disk by app or service" });
}

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
    // aaa is first by name but last by CPU; zzz is first by CPU but last by name.
    result = {
      ...result,
      data: { ...LIST, processes: [...LIST.processes, p({ pid: 3, name: "aaa", cpu_pct: 0 }), p({ pid: 4, name: "zzz", cpu_pct: 90 })] },
    };
    rerender(<MemoryRouter><ProcessesTab serverId="s1" /></MemoryRouter>);
    expect(names()[0]).toBe("aaa");
    expect(names()[0]).not.toBe("zzz");
    expect(names().at(-1)).toBe("zzz");
  });

  it("filters by the App select", async () => {
    const user = userEvent.setup();
    renderTab();
    await user.click(screen.getByRole("combobox", { name: "App" }));
    await user.click(await screen.findByRole("option", { name: "Host only" }));
    expect(names()).toEqual(["sshd", "bash"]);
    await user.click(screen.getByRole("combobox", { name: "App" }));
    await user.click(await screen.findByRole("option", { name: "Nextcloud" }));
    expect(names()).toEqual(["postgres"]);
  });

  it("drops an app filter whose app left the list", async () => {
    const user = userEvent.setup();
    const { rerender } = renderTab();
    await user.click(screen.getByRole("combobox", { name: "App" }));
    await user.click(await screen.findByRole("option", { name: "Nextcloud" }));
    expect(names()).toEqual(["postgres"]);
    result = { ...result, data: { ...LIST, processes: LIST.processes.filter((x) => x.pid !== 10) } };
    rerender(<MemoryRouter><ProcessesTab serverId="s1" /></MemoryRouter>);
    expect(names()).toEqual(["sshd", "bash"]);
    expect(screen.getByRole("combobox", { name: "App" })).toHaveTextContent("All processes");
  });

  it("expands a row's details with a keyboard-reachable button", async () => {
    const user = userEvent.setup();
    renderTab();
    const btn = screen.getByRole("button", { name: "Details for sshd" });
    expect(btn).toHaveAttribute("aria-expanded", "false");
    await user.click(btn);
    expect(btn).toHaveAttribute("aria-expanded", "true");
    const details = screen.getAllByRole("row").find((r) => within(r).queryAllByRole("term").length > 0)!;
    for (const label of ["App", "User", "PID", "Group", "Threads", "Uptime"]) {
      expect(within(details).getByText(label)).toBeInTheDocument();
    }
    await user.click(btn);
    expect(btn).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryAllByRole("term")).toHaveLength(0);
  });

  it("shows the full command line in a row's details", async () => {
    const user = userEvent.setup();
    const cmdline = "/usr/sbin/sshd -D -o ListenAddress=0.0.0.0 -o PermitRootLogin=no " + "x".repeat(200);
    result = { ...result, data: { ...LIST, processes: [p({ pid: 7, name: "sshd", cmdline })] } };
    renderTab();
    await user.click(screen.getByRole("button", { name: "Details for sshd" }));
    const details = screen.getAllByRole("row").find((r) => within(r).queryAllByRole("term").length > 0)!;
    const term = within(details).getByText("Command");
    const value = term.nextElementSibling as HTMLElement;
    expect(value.tagName).toBe("DD");
    expect(value).toHaveTextContent(cmdline);
    expect(value).toHaveClass("break-all");
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

  it("links the app and shows the group in the table", () => {
    renderTab();
    const row = screen.getAllByTestId("process-row").find((r) => within(r).queryByText("postgres"))!;
    expect(within(row).getByRole("link", { name: "Nextcloud" })).toHaveAttribute("href", "/server/s1/app/app-1");
    const sshd = screen.getAllByTestId("process-row").find((r) => within(r).queryByText("sshd"))!;
    expect(within(sshd).getByText("ssh.service")).toBeInTheDocument();
  });

  it("has a Group column and no per-process disk columns", () => {
    renderTab();
    const table = screen.getByRole("table");
    expect(within(table).getByRole("button", { name: "Sort by Group" })).toBeInTheDocument();
    expect(within(table).queryByRole("button", { name: "Sort by Read/s" })).not.toBeInTheDocument();
    expect(within(table).queryByRole("button", { name: "Sort by Write/s" })).not.toBeInTheDocument();
  });

  it("sorts by group name", async () => {
    const user = userEvent.setup();
    renderTab();
    await user.click(screen.getByRole("button", { name: "Sort by Group" }));
    // container 0123… (bash), Nextcloud (postgres), ssh.service (sshd)
    expect(names()).toEqual(["bash", "postgres", "sshd"]);
  });

  it("lists disk I/O per app or service, with a dash for unknown rates", () => {
    renderTab();
    const rows = within(groupList()).getAllByRole("button");
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent("Nextcloud");
    expect(rows[0]).toHaveTextContent("1.0 KB/s");
    expect(rows[0]).toHaveTextContent("2.0 KB/s");
    expect(rows[0]).toHaveTextContent("3 processes");
    expect(rows[1]).toHaveTextContent("ssh.service");
    expect(rows[1]).toHaveTextContent("service");
    expect(rows[1]).toHaveTextContent("1 process");
    expect(rows[2]).toHaveTextContent("container 0123456789ab");
    // No accounting at all: one dash, explained.
    expect(within(rows[2]).getByTitle("No disk accounting for this group")).toHaveTextContent("—");
    expect(rows[2]).toHaveAccessibleName(/No disk accounting for this group/);
  });

  it("spaces the disk card as a column", () => {
    renderTab();
    const card = screen.getByRole("heading", { name: "Disk by app or service" }).parentElement!;
    expect(card).toHaveClass("flex", "flex-col", "gap-2", "p-3.5");
  });

  it("reads the rates as text, not labels, and keeps list semantics", () => {
    renderTab();
    expect(groupList()).toHaveAttribute("role", "list");
    const row = within(groupList()).getAllByRole("button")[0];
    expect(row).toHaveAccessibleName(/Read\s*1\.0 KB\/s/);
    expect(row).toHaveAccessibleName(/Write\s*2\.0 KB\/s/);
    expect(row.querySelectorAll("[aria-label]")).toHaveLength(0);
  });

  it("titles the truncated Group cell with the full name", () => {
    renderTab();
    const sshd = screen.getAllByTestId("process-row").find((r) => within(r).queryByText("sshd"))!;
    expect(within(sshd).getByText("ssh.service")).toHaveAttribute("title", "ssh.service");
  });

  it("says a group's processes are beyond the busiest shown", async () => {
    const user = userEvent.setup();
    result = {
      ...result,
      data: { ...LIST, total: 9, groups: [...LIST.groups, g({ id: "far", label: "far.service", processes: 5 })] },
    };
    renderTab();
    await user.click(within(groupList()).getByRole("button", { name: /far\.service/ }));
    expect(screen.getByText("This group's processes are beyond the busiest 4 shown.")).toBeInTheDocument();
    expect(screen.queryByText("No processes match.")).not.toBeInTheDocument();
  });

  it("shows the first 8 groups and the rest on request", async () => {
    const user = userEvent.setup();
    const groups = Array.from({ length: 11 }, (_, i) => g({ id: `u${i}`, label: `unit${i}.service` }));
    result = { ...result, data: { ...LIST, groups } };
    renderTab();
    expect(within(groupList()).getAllByRole("button")).toHaveLength(8);
    await user.click(screen.getByRole("button", { name: "Show all (11)" }));
    expect(within(groupList()).getAllByRole("button")).toHaveLength(11);
    await user.click(screen.getByRole("button", { name: "Show fewer" }));
    expect(within(groupList()).getAllByRole("button")).toHaveLength(8);
  });

  it("offers no toggle for 8 groups or fewer", () => {
    renderTab();
    expect(screen.queryByRole("button", { name: /Show all/ })).not.toBeInTheDocument();
  });

  it("tells an app's containers apart, in the list and the chip", async () => {
    const user = userEvent.setup();
    result = {
      ...result,
      data: {
        ...LIST,
        processes: [...LIST.processes, p({ pid: 11, name: "mariadbd", app_id: "app-1", group: "c3" })],
        groups: [
          { ...LIST.groups[0], label: "wf-app-1-web-1" },
          g({ id: "c3", kind: "container", container_id: "bbbbbbbbbbbbbbbb", app_id: "app-1", label: "wf-app-1-db-1" }),
        ],
      },
    };
    renderTab();
    const rows = within(groupList()).getAllByRole("button");
    expect(rows[0]).toHaveTextContent("Nextcloud · web");
    expect(rows[1]).toHaveTextContent("Nextcloud · db");
    await user.click(rows[1]);
    expect(names()).toEqual(["mariadbd"]);
    expect(screen.getByText("Group: Nextcloud · db")).toBeInTheDocument();
  });

  it("filters the table to a group, with a chip, and clears it", async () => {
    const user = userEvent.setup();
    renderTab();
    await user.click(within(groupList()).getByRole("button", { name: /ssh\.service/ }));
    expect(names()).toEqual(["sshd"]);
    expect(within(groupList()).getByRole("button", { name: /ssh\.service/ })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("Group: ssh.service")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Clear group filter" }));
    expect(names()).toEqual(["sshd", "postgres", "bash"]);
    expect(screen.queryByText("Group: ssh.service")).not.toBeInTheDocument();
    expect(within(groupList()).getByRole("button", { name: /ssh\.service/ })).toHaveAttribute("aria-pressed", "false");
  });

  it("clears the group filter by clicking the active row again", async () => {
    const user = userEvent.setup();
    renderTab();
    const row = () => within(groupList()).getByRole("button", { name: /Nextcloud/ });
    await user.click(row());
    expect(names()).toEqual(["postgres"]);
    await user.click(row());
    expect(names()).toEqual(["sshd", "postgres", "bash"]);
  });

  it("drops a group filter whose group left the list", async () => {
    const user = userEvent.setup();
    const { rerender } = renderTab();
    await user.click(within(groupList()).getByRole("button", { name: /ssh\.service/ }));
    result = { ...result, data: { ...LIST, groups: LIST.groups.filter((x) => x.id !== "ssh") } };
    rerender(<MemoryRouter><ProcessesTab serverId="s1" /></MemoryRouter>);
    expect(names()).toEqual(["sshd", "postgres", "bash"]);
    expect(screen.queryByRole("button", { name: "Clear group filter" })).not.toBeInTheDocument();
  });

  it("matches the group name in the text filter", async () => {
    const user = userEvent.setup();
    renderTab();
    await user.type(screen.getByRole("searchbox", { name: "Filter processes" }), "ssh.service");
    expect(names()).toEqual(["sshd"]);
  });

  it("says disk I/O needs cgroup v2 when there are no groups", () => {
    result = { ...result, data: { ...LIST, processes: LIST.processes.map((x) => ({ ...x, group: undefined })), groups: [] } };
    renderTab();
    expect(screen.getByText("Disk I/O per app or service needs cgroup v2 on this server.")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Disk by app or service" })).not.toBeInTheDocument();
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

  it("shows an error without the skeleton when nothing has loaded", () => {
    result = { data: null, error: "This server's agent is too old to list processes. Update the agent.", updatedAt: null, refresh: vi.fn() };
    renderTab();
    expect(screen.getByText("This server's agent is too old to list processes. Update the agent.")).toBeInTheDocument();
    expect(screen.queryByTestId("processes-loading")).not.toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("says when the list is cut", () => {
    result = { ...result, data: { ...LIST, total: 2500 } };
    renderTab();
    expect(screen.getByText(/busiest 4 of 2500/)).toBeInTheDocument();
  });
});
