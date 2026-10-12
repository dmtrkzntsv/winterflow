import { act, render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AlertToasts } from "@/components/alerts/alert-toasts";
import { fakeNotifications } from "@/test/notifications";

const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

describe("AlertToasts", () => {
  it("toasts fired alerts red and resolved ones green, ignoring other events", () => {
    const sse = fakeNotifications();
    render(
      <sse.Provider>
        <AlertToasts />
      </sse.Provider>,
    );
    act(() => {
      sse.emit({ type: "alert", ref: "", payload: { kind: "fired", title: "🔥 prod-1 · CPU 93% > 90% for 10m" } });
      sse.emit({ type: "alert", ref: "", payload: { kind: "resolved", title: "✅ Resolved · prod-1 · CPU 41%" } });
      sse.emit({ type: "apps_status", ref: "", payload: {} });
    });
    expect(toast.error).toHaveBeenCalledWith("🔥 prod-1 · CPU 93% > 90% for 10m");
    expect(toast.success).toHaveBeenCalledWith("✅ Resolved · prod-1 · CPU 41%");
    expect(toast.error).toHaveBeenCalledTimes(1);
  });
});
