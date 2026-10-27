import { describe, expect, it } from "vitest";

import { fromLocation, loginPath, returnTarget, safePath } from "@/lib/return-path";

describe("safePath", () => {
  it.each(["/", "/server/s1", "/oauth/authorize?client_id=c&state=s", "/a#b"])("keeps %s", (p) => {
    expect(safePath(p)).toBe(p);
  });

  it.each([
    "//evil.example",
    "/\\evil.example",
    "https://evil.example",
    "server/s1",
    "/\t/evil.example",
    "/x\n",
    "",
    undefined,
    7,
  ])("refuses %s", (p) => {
    expect(safePath(p)).toBeNull();
  });
});

describe("fromLocation", () => {
  it("joins path, query and hash", () => {
    expect(fromLocation({ pathname: "/a", search: "?b=1", hash: "#c" })).toBe("/a?b=1#c");
  });

  it.each([
    { pathname: "/", search: "/evil.example" },
    { pathname: "/", hash: "/evil.example" },
    { pathname: "/x", search: "y" },
    { pathname: 5 },
  ])("refuses %o", (from) => {
    expect(fromLocation(from)).toBeNull();
  });
});

describe("loginPath", () => {
  it("carries next, encoded", () => {
    expect(loginPath("/oauth/authorize?client_id=c&state=s")).toBe(
      "/login?next=%2Foauth%2Fauthorize%3Fclient_id%3Dc%26state%3Ds",
    );
  });

  it("is plain /login for home or an unsafe next", () => {
    expect(loginPath("/")).toBe("/login");
    expect(loginPath("//evil.example")).toBe("/login");
  });
});

describe("returnTarget", () => {
  it("prefers ?next=, then the guard's location, then home", () => {
    expect(returnTarget(new URLSearchParams("next=/a"), { pathname: "/b" })).toBe("/a");
    expect(returnTarget(new URLSearchParams(), { pathname: "/b" })).toBe("/b");
    expect(returnTarget(new URLSearchParams(), undefined)).toBe("/");
  });

  it("goes home for an unsafe ?next= rather than falling back", () => {
    expect(returnTarget(new URLSearchParams("next=//evil.example"), { pathname: "/b" })).toBe("/");
  });
});
