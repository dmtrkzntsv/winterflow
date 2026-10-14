import { describe, expect, it } from "vitest";

import { AppStatusCode } from "@/context/apps-context-base";
import { appStatus, backupStatus, serverStatus } from "@/lib/status";

describe("appStatus", () => {
  it.each([
    [AppStatusCode.Active, "ok", "Running"],
    [AppStatusCode.Idle, "warn", "Idle"],
    [AppStatusCode.Restarting, "warn", "Restarting"],
    [AppStatusCode.Problematic, "bad", "Problematic"],
    [AppStatusCode.Stopped, "idle", "Stopped"],
    [AppStatusCode.Unknown, "idle", "Unknown"],
  ])("maps code %i to %s %s", (code, tone, label) => {
    expect(appStatus(code)).toMatchObject({ tone, label });
  });

  it("reads a missing or unknown code as Unknown, never as down", () => {
    expect(appStatus(undefined)).toEqual({ tone: "idle", label: "Unknown" });
    expect(appStatus(42)).toEqual({ tone: "idle", label: "Unknown" });
  });

  it("pulses only while restarting", () => {
    expect(appStatus(AppStatusCode.Restarting).pulse).toBe(true);
    expect(appStatus(AppStatusCode.Active).pulse).toBeFalsy();
  });
});

describe("serverStatus", () => {
  it("calls the embedded server of a standalone install this server", () => {
    expect(serverStatus({ liveness: undefined, lastSeenAt: null, standalone: true })).toEqual({
      tone: "ok",
      label: "This server",
    });
  });

  it("is online while the agent is connected", () => {
    expect(serverStatus({ liveness: "online", lastSeenAt: null, standalone: false })).toEqual({ tone: "ok", label: "Online" });
  });

  it("tells a server that never connected from one that went away", () => {
    expect(serverStatus({ liveness: undefined, lastSeenAt: null, standalone: false })).toEqual({
      tone: "idle",
      label: "Never connected",
    });
    expect(serverStatus({ liveness: "unknown", lastSeenAt: "2026-10-01T00:00:00Z", standalone: false })).toEqual({
      tone: "bad",
      label: "Offline",
    });
  });
});

describe("backupStatus", () => {
  it.each([
    ["ok", "ok", "OK"],
    ["running", "ok", "Running"],
    ["never", "idle", "Not run yet"],
    ["off", "idle", "Off"],
    ["late", "warn", "Late"],
    ["failing", "bad", "Failing"],
  ] as const)("maps %s to %s %s", (state, tone, label) => {
    expect(backupStatus(state)).toMatchObject({ tone, label });
  });
});
