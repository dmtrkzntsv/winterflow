import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import SecretRequestPage from "@/pages/secret-request";
import { ApiError } from "@/lib/api";
import { fakeAPI } from "@/test/fake-api";

// The fake encryption is visibly not the plaintext: the posted body must
// carry only what encryptSecret returned.
const encryptSecret = vi.hoisted(() => vi.fn(async (plain: string, key: string) => `ct(${key}):${plain.length}`));
vi.mock("@/lib/ecies", () => ({ encryptSecret }));

const TOKEN = "wfx_abc";
const VIEW = {
  server_id: "s1",
  server_name: "homelab",
  app_id: "",
  app_name: "",
  names: ["API_KEY", "DB_PASSWORD"],
  notes: { API_KEY: "From the provider's dashboard <b>not html</b>" },
  message: "Paperless needs these to start.",
  expires_at: "2026-10-28T12:00:00Z",
  public_key: "PUBKEY",
};

function renderPage() {
  return render(
    <MemoryRouter initialEntries={[`/secrets/${TOKEN}`]}>
      <Routes>
        <Route path="/secrets/:token" element={<SecretRequestPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  encryptSecret.mockClear();
});

describe("SecretRequestPage", () => {
  it("shows the request as plain text, one password field per name", async () => {
    const api = fakeAPI({ "/api/v1/secret-request/view": VIEW });
    renderPage();
    expect(await screen.findByText("Paperless needs these to start.")).toBeInTheDocument();
    expect(screen.getByText(/a new app on homelab/)).toBeInTheDocument();
    expect(screen.getByText("From the provider's dashboard <b>not html</b>")).toBeInTheDocument();
    for (const name of VIEW.names) {
      expect(screen.getByLabelText(name)).toHaveAttribute("type", "password");
    }
    expect(Object.fromEntries(new URLSearchParams(api.calls[0].query))).toEqual({ token: TOKEN });
    expect(screen.getByRole("button", { name: "Submit" })).toBeDisabled();
  });

  it("encrypts every value before posting it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/secret-request/view": VIEW, "/api/v1/secret-request/submit": null });
    renderPage();
    await user.type(await screen.findByLabelText("API_KEY"), "sk-live-123");
    await user.type(screen.getByLabelText("DB_PASSWORD"), "hunter2");
    await user.click(screen.getByRole("button", { name: "Submit" }));
    expect(await screen.findByText("Submitted — your agent can continue")).toBeInTheDocument();
    expect(encryptSecret).toHaveBeenCalledWith("sk-live-123", "PUBKEY");
    expect(encryptSecret).toHaveBeenCalledWith("hunter2", "PUBKEY");
    expect(api.posted("/api/v1/secret-request/submit")).toEqual([
      { token: TOKEN, values: { API_KEY: "ct(PUBKEY):11", DB_PASSWORD: "ct(PUBKEY):7" } },
    ]);
    const init = api.fetchMock.mock.calls.find(([u]) => String(u).includes("/submit"))?.[1];
    const body = String(init?.body);
    expect(body).not.toContain("sk-live-123");
    expect(body).not.toContain("hunter2");
    expect(new Headers(init?.headers).get("X-Winterflow-Request")).toBe("1");
  });

  it("shows one generic message for a link it can't use", async () => {
    fakeAPI({ "/api/v1/secret-request/view": new ApiError("this link is not valid", 404) });
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("It may have expired or been used already");
    expect(screen.queryByRole("button", { name: "Submit" })).not.toBeInTheDocument();
  });

  it("refuses to post without the server's key", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/secret-request/view": { ...VIEW, names: ["API_KEY"], public_key: "" } });
    renderPage();
    await user.type(await screen.findByLabelText("API_KEY"), "x");
    await user.click(screen.getByRole("button", { name: "Submit" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("public key isn't available"));
    expect(api.posted("/api/v1/secret-request/submit")).toEqual([]);
    expect(encryptSecret).not.toHaveBeenCalled();
  });
});
