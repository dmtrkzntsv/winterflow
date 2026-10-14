import { describe, expect, it } from "vitest";

import {
  entryBinding,
  entryHostPorts,
  firstFreeHostPort,
  hostPortsIn,
  interpolate,
  isLoopback,
  publishedHostPorts,
  publishedPorts,
  randomHostPort,
  rewriteEntry,
  rewriteHostPorts,
} from "@/lib/host-ports";

describe("entryHostPorts", () => {
  it.each([
    ['"8080:80"', [8080]],
    ["8080:80", [8080]],
    ["'127.0.0.1:8081:80'", [8081]],
    ['"8082:80/udp"', [8082]],
    ['"[::1]:8083:80"', [8083]],
    ['"8084-8086:80-82"', [8084, 8085, 8086]],
    ['"${PORT}:80"', []],
    ['"127.0.0.1:${PORT}:80"', []],
    ['"80"', []],
    ['"127.0.0.1::80"', []],
    ["", []],
    ['"abc:80"', []],
  ])("reads %s", (entry, ports) => {
    expect(entryHostPorts(entry)).toEqual(ports);
  });
});

describe("publishedHostPorts", () => {
  it("reads the short syntax of every service, block and flow lists", () => {
    const compose = `services:
  web:
    image: nginx
    ports:
      # host:container
      - "8088:80" # the site
      - 127.0.0.1:8090:80
  api:
    ports: ["9000:9000", "\${API_PORT}:8080"]
  db:
    ports:
    - "5432:5432"
    environment:
      - "NOT_A_PORT=1:2"
  long:
    ports:
      - target: 80
        published: 8095
volumes:
  data: {}
`;
    expect(publishedHostPorts(compose)).toEqual([8088, 8090, 9000, 5432]);
  });

  it("finds nothing without a ports key", () => {
    expect(publishedHostPorts("services:\n  app:\n    image: x\n")).toEqual([]);
  });
});

describe("entryBinding", () => {
  it.each([
    ['"8080:80"', { host: "", ports: [8080], proto: "" }],
    ["127.0.0.1:8081:80", { host: "127.0.0.1", ports: [8081], proto: "" }],
    ['"0.0.0.0:8082:80/tcp"', { host: "0.0.0.0", ports: [8082], proto: "tcp" }],
    ['"[::1]:8083:80"', { host: "::1", ports: [8083], proto: "" }],
    ['"51820:51820/UDP"', { host: "", ports: [51820], proto: "udp" }],
    ['"[::1]8083:80"', { host: "", ports: [], proto: "" }],
    ['"${PORT}:80"', { host: "", ports: [], proto: "" }],
  ])("reads %s", (entry, binding) => {
    expect(entryBinding(entry)).toEqual(binding);
  });
});

describe("publishedPorts", () => {
  it("names each port's service and bind address", () => {
    const compose = `services:
  web:
    image: nginx
    ports:
      - "8090:80"
      - 127.0.0.1:8091:80
  "api":
    ports: ["9000:9000"]
`;
    expect(publishedPorts(compose)).toEqual([
      { service: "web", host: "", port: 8090 },
      { service: "web", host: "127.0.0.1", port: 8091 },
      { service: "api", host: "", port: 9000 },
    ]);
  });

  it("gives a range one entry, and names a protocol other than tcp", () => {
    const compose = `services:
  media:
    ports:
      - "8000-8010:8000-8010"
      - "51820:51820/udp"
      - "8443:443/tcp"
`;
    const ports = publishedPorts(compose);
    expect(ports).toEqual([
      { service: "media", host: "", port: 8000, last: 8010 },
      { service: "media", host: "", port: 51820, proto: "udp" },
      { service: "media", host: "", port: 8443 },
    ]);
    expect(hostPortsIn(ports[0])).toHaveLength(11);
    expect(hostPortsIn(ports[1])).toEqual([51820]);
    expect(publishedHostPorts(compose)).toHaveLength(13);
  });

  it("has no service for a top-level ports key", () => {
    expect(publishedPorts('ports: ["80:80"]')).toEqual([{ service: "", host: "", port: 80 }]);
  });

  it("reads interpolated ports through the variables given", () => {
    const compose = `services:
  web:
    ports:
      - "127.0.0.1:\${PORT}:8080"
      - "$ADMIN:9000"
  db:
    ports: ["\${DB_PORT:-5433}:5432", "\${MISSING}:80"]
`;
    expect(publishedPorts(compose, { PORT: "8095", ADMIN: "8096" })).toEqual([
      { service: "web", host: "127.0.0.1", port: 8095 },
      { service: "web", host: "", port: 8096 },
      { service: "db", host: "", port: 5433 },
    ]);
    // Without variables only the default resolves.
    expect(publishedPorts(compose)).toEqual([{ service: "db", host: "", port: 5433 }]);
  });
});

describe("interpolate", () => {
  it("resolves references the way compose does", () => {
    const vars = { PORT: "8080", EMPTY: "" };
    expect(interpolate("${PORT}:80", vars)).toBe("8080:80");
    expect(interpolate("$PORT:80", vars)).toBe("8080:80");
    expect(interpolate("${EMPTY:-9000}:80", vars)).toBe("9000:80");
    expect(interpolate("${EMPTY-9000}:80", vars)).toBe(":80");
    expect(interpolate("${UNSET-9000}:80", vars)).toBe("9000:80");
    expect(interpolate("${UNSET:-}:80", vars)).toBe(":80");
  });

  it("leaves what it can't resolve, and an escaped $$, as written", () => {
    expect(interpolate("${UNSET}:80", {})).toBe("${UNSET}:80");
    expect(interpolate("$$PORT:80", { PORT: "1" })).toBe("$$PORT:80");
    expect(interpolate("${PORT:?required}:80", {})).toBe("${PORT:?required}:80");
    expect(interpolate("${PORT:?required}:80", { PORT: "" })).toBe("${PORT:?required}:80");
    expect(interpolate("${PORT:?required}:80", { PORT: "1" })).toBe("1:80");
    expect(interpolate("${PORT?required}:80", { PORT: "1" })).toBe("1:80");
  });
});

describe("rewriteEntry", () => {
  const toParam = (p: number) => (p === 9999 ? null : "${PORT}");
  it.each([
    ['"8090:80"', '"${PORT}:80"'],
    ["'8090:80'", "'${PORT}:80'"],
    ["8090:80", '"${PORT}:80"'],
    ["127.0.0.1:8090:80/tcp", '"127.0.0.1:${PORT}:80/tcp"'],
    ['"[::1]:8090:80"', '"[::1]:${PORT}:80"'],
    [' "8090:80"', ' "${PORT}:80"'],
    ['"8090-8091:80-81"', '"8090-8091:80-81"'],
    ['"${PORT}:80"', '"${PORT}:80"'],
    ['"80"', '"80"'],
    ['"[::1]8090:80"', '"[::1]8090:80"'],
    ['"[::1]:1:2:3"', '"[::1]:1:2:3"'],
    ['"9999:80"', '"9999:80"'],
  ])("%s → %s", (entry, out) => {
    expect(rewriteEntry(entry, toParam)).toBe(out);
  });
});

describe("rewriteHostPorts", () => {
  it("rewrites block and flow lists, keeping comments, line breaks and the rest", () => {
    const compose = [
      "services:",
      "  web:",
      "    ports:",
      '      - "8090:80" # the site',
      "      - 127.0.0.1:8091:80",
      "    environment:",
      "      - A=1:2",
      "  api:",
      '    ports: ["9000:9000", "${X}:1"] # api',
      "",
    ].join("\r\n");
    const names: Record<number, string> = { 8090: "${PORT}", 8091: "${PORT_2}", 9000: "${PORT_3}" };
    expect(rewriteHostPorts(compose, (p) => names[p] ?? null).split("\r\n")).toEqual([
      "services:",
      "  web:",
      "    ports:",
      '      - "${PORT}:80" # the site',
      '      - "127.0.0.1:${PORT_2}:80"',
      "    environment:",
      "      - A=1:2",
      "  api:",
      '    ports: ["${PORT_3}:9000", "${X}:1"] # api',
      "",
    ]);
  });
});

describe("isLoopback", () => {
  it.each([
    ["127.0.0.1", true],
    ["127.1.2.3", true],
    ["::1", true],
    ["localhost", true],
    ["", false],
    ["0.0.0.0", false],
    ["192.168.1.66", false],
  ])("%s → %s", (host, loopback) => {
    expect(isLoopback(host)).toBe(loopback);
  });
});

describe("firstFreeHostPort", () => {
  it("is the first port of 8080-8999 nobody publishes", () => {
    expect(firstFreeHostPort(new Set())).toBe(8080);
    expect(firstFreeHostPort(new Set([8080, 8081, 8083]))).toBe(8082);
    const all = new Set(Array.from({ length: 920 }, (_, i) => 8080 + i));
    expect(firstFreeHostPort(all)).toBeUndefined();
  });
});

describe("randomHostPort", () => {
  it("stays within 8080-8999", () => {
    expect(randomHostPort(() => 0)).toBe(8080);
    expect(randomHostPort(() => 0.9999999)).toBe(8999);
  });
});
