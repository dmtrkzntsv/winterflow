import { describe, expect, it } from "vitest";

import { appPublishedPorts, portsOf } from "@/hooks/use-used-host-ports";
import type { AppDetailPayload } from "@/context/apps-context-base";

const b64 = (s: string) => btoa(s);

// detail is an app.get answer: a compose file publishing ${PORT} and a
// secret port nobody can read.
const detail = (variables: AppDetailPayload["app"]["variables"]): AppDetailPayload => ({
  app: {
    app_id: "a1",
    config: "",
    files: [
      { name: "compose.yml", content: b64('services:\n  web:\n    ports:\n      - "127.0.0.1:${PORT}:8080"\n      - "${ADMIN}:9000"\n') },
      { name: "notes.txt", content: b64('ports: ["7000:7000"]') },
    ],
    variables,
  },
  revision: 1,
  available_revisions: null,
});

describe("appPublishedPorts", () => {
  it("reads ${PORT}-style ports through the app's plain variables, never its secrets", () => {
    const d = detail([
      { name: "PORT", content: b64("8095") },
      { name: "ADMIN", content: b64("<encrypted>"), encrypted: true },
    ]);
    expect(appPublishedPorts(d)).toEqual([{ service: "web", host: "127.0.0.1", port: 8095 }]);
    expect(portsOf(d)).toEqual([8095]);
  });

  it("leaves out a port no variable sets", () => {
    expect(appPublishedPorts(detail(null))).toEqual([]);
  });
});
