import { apiBase } from "@/config";
import { createJsonClient, jsonPost } from "@/lib/api";
import { relativeTime } from "@/lib/utils";

// ConnectedApp is an MCP client the user approved (an OAuth grant).
export type ConnectedApp = {
  grant_id: string;
  client_name: string;
  redirect_host: string;
  is_loopback: boolean;
  created_at: string;
  last_used_at: string;
};

// Identity is a sign-in provider account linked to the user.
export type Identity = {
  provider: string;
  email: string;
  created_at: string;
};

export type AccountApi = {
  connectedApps(): Promise<ConnectedApp[]>;
  revokeConnectedApp(grantId: string): Promise<void>;
  identities(): Promise<Identity[]>;
  unlinkIdentity(provider: string): Promise<void>;
};

// createAccountApi is the client of the account's connected apps and
// linked sign-ins (fetchImpl: tests).
export function createAccountApi(fetchImpl?: typeof fetch): AccountApi {
  const { call } = createJsonClient(apiBase, fetchImpl);
  return {
    connectedApps: async () => (await call<ConnectedApp[] | null>("/api/v1/user/get-connected-apps")) ?? [],
    revokeConnectedApp: async (grantId) => {
      await call<null>("/api/v1/user/revoke-connected-app", jsonPost({ grant_id: grantId }));
    },
    identities: async () => (await call<Identity[] | null>("/api/v1/user/get-identities")) ?? [],
    unlinkIdentity: async (provider) => {
      await call<null>("/api/v1/user/unlink-identity", jsonPost({ provider }));
    },
  };
}

// connectedAppMeta is a connected app's quiet line: when it was approved
// and last used, relative to now.
export function connectedAppMeta(a: ConnectedApp, now: number): string {
  return [
    `Connected ${relativeTime(new Date(a.created_at), now)}`,
    `Last used ${relativeTime(new Date(a.last_used_at), now)}`,
  ].join(" · ");
}

// connectedAppWhere says where a connected app's answers go: its host, or
// that it runs on the user's own computer.
export function connectedAppWhere(a: ConnectedApp): string {
  if (a.is_loopback) return "On your computer";
  return a.redirect_host || "Unknown address";
}

// PROVIDER_NAMES name the providers when the instance's state can't be
// read (an OIDC provider's own name comes from that state).
const PROVIDER_NAMES: Record<string, string> = { google: "Google", apple: "Apple", oidc: "Single sign-on" };

export function providerName(id: string, known: { id: string; name: string }[] = []): string {
  return known.find((p) => p.id === id)?.name ?? PROVIDER_NAMES[id] ?? id;
}
