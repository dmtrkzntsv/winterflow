import { describe, expect, it } from "vitest";

import {
  entryBinding,
  entryHostPorts,
  firstFreeHostPort,
  isLoopback,
  publishedHostPorts,
  publishedPorts,
  randomHostPort,
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
    ['"8080:80"', { host: "", ports: [8080] }],
    ["127.0.0.1:8081:80", { host: "127.0.0.1", ports: [8081] }],
    ['"0.0.0.0:8082:80/tcp"', { host: "0.0.0.0", ports: [8082] }],
    ['"[::1]:8083:80"', { host: "::1", ports: [8083] }],
    ['"[::1]8083:80"', { host: "", ports: [] }],
    ['"${PORT}:80"', { host: "", ports: [] }],
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

  it("has no service for a top-level ports key", () => {
    expect(publishedPorts('ports: ["80:80"]')).toEqual([{ service: "", host: "", port: 80 }]);
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
