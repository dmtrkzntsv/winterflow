import { describe, expect, it } from "vitest";

import type { App } from "@/context/apps-context-base";
import { composeStart, createdApp, gitStart, parseNewAppSource, withHostPort } from "@/lib/new-app";
import { DEFAULT_COMPOSE } from "@/types/app-config";

const app = (id: string, name: string) => ({ id, name, serverId: "s1", version: "1", icon: "", color: "", createdAt: "" }) as App;

describe("parseNewAppSource", () => {
  it("reads the three sources and nothing else", () => {
    expect(parseNewAppSource("template")).toBe("template");
    expect(parseNewAppSource("compose")).toBe("compose");
    expect(parseNewAppSource("git")).toBe("git");
    expect(parseNewAppSource("GIT")).toBeNull();
    expect(parseNewAppSource("")).toBeNull();
    expect(parseNewAppSource(null)).toBeNull();
  });
});

describe("createdApp", () => {
  it("finds the new app, not an older one of the same name", () => {
    const list = [app("old", "shop"), app("new", "shop"), app("x", "blog")];
    expect(createdApp(list, new Set(["old", "x"]), "shop")?.id).toBe("new");
  });

  it("finds nothing when the answer doesn't list it yet", () => {
    expect(createdApp([app("old", "shop")], new Set(["old"]), "shop")).toBeUndefined();
  });
});

describe("starting states", () => {
  it("starts a compose app with the example file and one blank address", () => {
    const s = composeStart();
    expect(s.config.files.map((f) => f.filename)).toEqual(["compose.yml"]);
    expect(Object.values(s.files)).toEqual([DEFAULT_COMPOSE]);
    expect(s.config.ingress?.domains).toEqual([expect.objectContaining({ domain: "", upstream_port: "" })]);
    expect(s.config.source).toBeUndefined();
  });

  it("publishes the given host port, and moves it only while the file is untouched", () => {
    const s = composeStart(8123);
    const id = s.config.files[0].id;
    expect(s.files[id]).toContain('- "8123:80"');
    const moved = withHostPort(s, 8123, 8080);
    expect(moved.files[id]).toBe(DEFAULT_COMPOSE);
    expect(withHostPort(s, 8123, 8123)).toBe(s);
    const edited = { ...s, files: { [id]: s.files[id] + "# mine\n" } };
    expect(withHostPort(edited, 8123, 8080)).toBe(edited);
    expect(withHostPort(gitStart(), 8123, 8080).files).toEqual({});
  });

  it("starts a Git app with no files and the default source", () => {
    const s = gitStart();
    expect(s.config.files).toEqual([]);
    expect(s.files).toEqual({});
    expect(s.config.source).toEqual({ repo_url: "", branch: "main", compose_path: "", auto_update: true, poll_seconds: 120 });
  });
});
