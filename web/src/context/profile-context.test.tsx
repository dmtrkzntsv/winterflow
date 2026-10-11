import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AuthContext, type AuthContextValue } from "@/context/auth-context-base";
import { ProfileProvider } from "@/context/profile-context";
import { useProfile } from "@/context/use-profile";

// Shows exactly what the context hands its consumers.
function Probe() {
  const value = useProfile();
  return <pre data-testid="value">{JSON.stringify(value)}</pre>;
}

function renderProvider(isAuthenticated: boolean) {
  const auth = { isAuthenticated } as AuthContextValue;
  return render(
    <AuthContext.Provider value={auth}>
      <ProfileProvider>
        <Probe />
      </ProfileProvider>
    </AuthContext.Provider>,
  );
}

describe("ProfileProvider", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("exposes the loaded profile and no role or admin flag", async () => {
    const profile = { user_id: "u-1", name: "Ann", email: "ann@example.test", must_change_password: false };
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ success: true, message: "profile", data: profile }))),
    );
    renderProvider(true);

    const value = await screen.findByText(/"user_id":"u-1"/);
    const parsed = JSON.parse(value.textContent ?? "{}");
    expect(parsed.profile).toEqual(profile);
    expect(parsed.loading).toBe(false);
    expect(Object.keys(parsed)).not.toContain("isAdmin");
  });

  it("has no profile when signed out", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    renderProvider(false);

    expect(await screen.findByText(/"loading":false/)).toHaveTextContent('"profile":null');
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
