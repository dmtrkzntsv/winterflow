import { describe, expect, it } from "vitest";

import { providerStartURL, signInErrorText } from "@/lib/sign-in";

describe("signInErrorText", () => {
  it("explains the callback's codes", () => {
    expect(signInErrorText("account_exists")).toMatch(/already exists/);
    expect(signInErrorText("invalid_state")).toMatch(/expired/);
  });

  it("falls back for an unknown code", () => {
    expect(signInErrorText("nope")).toBe("The sign-in didn't work. Try again.");
  });
});

describe("providerStartURL", () => {
  it("names the mode and the way back", () => {
    expect(providerStartURL("https://wf.test", "google", "link", "/settings/identities")).toBe(
      "https://wf.test/auth/oauth/google/start?mode=link&return_to=%2Fsettings%2Fidentities",
    );
  });
});
