import { describe, expect, it } from "vitest";

import { buildSavePayload, editorErrors, stateFromDetail } from "@/lib/app-editor-io";

const b64 = (s: string) => btoa(s);

function loaded() {
  return stateFromDetail("app-1", {
    app: {
      app_id: "app-1",
      config: b64(
        JSON.stringify({
          name: "notes",
          files: [{ filename: "compose.yml" }],
          variables: [],
          ingress: { domains: [{ domain: "notes.example.com", expose: "app:8080", ssl: true }], redirects: [] },
          ports: { "app:8080/http": { host: 20013, pinned: false }, "db:5432/tcp": { host: 5432, pinned: true } },
        }),
      ),
      files: [{ name: "compose.yml", content: b64("services: {}") }],
      variables: [],
    },
    expose: [
      { id: "app:8080/http", container: "app", port: 8080, protocol: "http", host_port: 20013, bind: "127.0.0.1", pinned: false },
      { id: "db:5432/tcp", container: "db", port: 5432, protocol: "tcp", host_port: 5432, bind: "0.0.0.0", pinned: true },
    ],
    revision: 1,
    available_revisions: null,
  });
}

describe("app editor io: expose and pins", () => {
  it("reads the expose entries, the ports and an expose target", () => {
    const s = loaded();
    expect(s.expose).toHaveLength(2);
    expect(s.config.ports?.["db:5432/tcp"]).toEqual({ host: 5432, pinned: true });
    expect(s.config.ingress?.domains[0]).toMatchObject({ domain: "notes.example.com", expose: "app:8080" });
    expect(editorErrors(s)).toEqual({});
  });

  it("sends only the pins, and an expose target without a port", async () => {
    const payload = await buildSavePayload(loaded(), async () => "PUB", "app-1");
    expect(payload.config.ports).toEqual({ "db:5432/tcp": { host: 5432, pinned: true } });
    expect(payload.config.ingress?.domains).toEqual([{ domain: "notes.example.com", expose: "app:8080", ssl: true }]);
  });

  it("refuses a target that is not a web port and a bad or shared pin", () => {
    const s = loaded();
    s.config.ingress!.domains[0].expose = "db:5432";
    s.config.ports = { "app:8080/http": { host: 5432, pinned: true }, "db:5432/tcp": { host: "", pinned: true } };
    const id = s.config.ingress!.domains[0].id;
    expect(editorErrors(s)).toEqual({
      [`port:${id}`]: "Pick one of the app's web ports.",
      "pin:db:5432/tcp": "Enter a port from 1 to 65535.",
    });
    s.config.ports = { "app:8080/http": { host: 5432, pinned: true }, "db:5432/tcp": { host: 5432, pinned: true } };
    expect(editorErrors(s)["pin:db:5432/tcp"]).toBe("app:8080/http is pinned to this port already.");
  });

  it("neither checks nor sends a pin of an entry that is gone", async () => {
    const s = loaded();
    s.config.ports = {
      "app:8080/http": { host: 5432, pinned: true },
      "gone:1/tcp": { host: 5432, pinned: true },
      "old:2/tcp": { host: "", pinned: true },
    };
    expect(editorErrors(s)).toEqual({});
    const payload = await buildSavePayload(s, async () => "PUB", "app-1");
    expect(payload.config.ports).toEqual({ "app:8080/http": { host: 5432, pinned: true } });
  });
});
