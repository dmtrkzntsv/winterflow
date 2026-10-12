import { render, renderHook, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SharedVariablesHint, useSharedNames } from "@/components/shared-variables-hint";
import type { ServerVariables, SharedVariablesApi } from "@/lib/shared-variables";

describe("SharedVariablesHint", () => {
  it("lists names and scopes, collapsed by default, never values", () => {
    render(<SharedVariablesHint shared={[{ name: "SMTP_PASS", scope: "org" }, { name: "WF_APP_DIR", scope: "builtin" }]} />);
    const summary = screen.getByText("Shared variables available to this app (2)");
    expect(summary.closest("details")).not.toHaveAttribute("open");
    expect(screen.getByText("SMTP_PASS")).toBeInTheDocument();
    expect(screen.getByText("org")).toBeInTheDocument();
    expect(screen.getByText("built-in")).toBeInTheDocument();
  });

  it("renders nothing without shared names", () => {
    const { container } = render(<SharedVariablesHint shared={[]} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("useSharedNames", () => {
  it("loads the server's shared names once per server", async () => {
    const listing: ServerVariables = {
      variables: [],
      inherited: [{ id: "o", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>", description: "", updated_at: "", updated_by: "", missing_on: [], overridden: false }],
      builtins: [],
      missing_org_secrets: [],
      sync: { server_id: "s", name: "s", has_key: true, revision: 0, agent_revision: null, state: "pending" },
    };
    const api = { listServer: vi.fn().mockResolvedValue(listing) } as unknown as SharedVariablesApi;
    const { result } = renderHook(() => useSharedNames("s1", api));
    await waitFor(() => expect(result.current).toEqual([{ name: "SMTP_PASS", scope: "org" }]));
    expect(api.listServer).toHaveBeenCalledWith("s1");
  });

  it("yields nothing when the listing fails", async () => {
    const api = { listServer: vi.fn().mockRejectedValue(new Error("down")) } as unknown as SharedVariablesApi;
    const { result } = renderHook(() => useSharedNames("s1", api));
    await waitFor(() => expect(api.listServer).toHaveBeenCalled());
    expect(result.current).toEqual([]);
  });
});
