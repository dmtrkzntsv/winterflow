import { useCallback, useMemo } from "react";

import { useNotifications } from "@/context/use-notifications";
import { useServers } from "@/context/use-servers";
import { encryptSecret } from "@/lib/ecies";
import type { DomainTLS, RegisteredDomain } from "@/lib/domains";
import { apiBaseUrl } from "@/config";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

export type Registry = { address: string };
export type Network = {
  id?: string;
  name: string;
  driver?: string;
  scope?: string;
};

export type Volume = {
  name: string;
  driver?: string;
  app_id?: string;
  project?: string;
  in_use: boolean;
  orphan: boolean;
  anonymous: boolean;
};

type Accepted = { data?: { request_id?: string } };

// useDocker exposes registry/network/volume operations. Each dispatches an agent
// command and awaits its SSE result (the agent's Docker daemon is the source of
// truth, so even list reads round-trip).
export function useDocker() {
  const { activeServerId } = useServers();
  const { waitFor } = useNotifications();

  const serverId = activeServerId;

  // dispatch sends a request, awaits its SSE result, and returns the payload.
  const dispatch = useCallback(
    async <T>(
      path: string,
      opts: { method?: string; body?: unknown; query?: boolean },
    ): Promise<T> => {
      if (!serverId) throw new Error("No active server");
      const sep = path.includes("?") ? "&" : "?";
      const url = `${base}${path}${sep}server_id=${encodeURIComponent(serverId)}`;
      const res = await fetch(url, {
        method: opts.method ?? "GET",
        credentials: "include",
        headers: opts.body ? { "Content-Type": "application/json" } : undefined,
        body: opts.body ? JSON.stringify(opts.body) : undefined,
      });
      if (!res.ok) throw new Error(`Request failed: ${res.status}`);
      const accepted = (await res.json()) as Accepted;
      const ref = accepted.data?.request_id;
      if (!ref) throw new Error("No request id");
      const result = await waitFor(ref);
      if (result.status && result.status !== 0) {
        throw new Error(result.error || "Operation failed");
      }
      return result.payload as T;
    },
    [serverId, waitFor],
  );

  const getPublicKey = useCallback(async (): Promise<string> => {
    if (!serverId) throw new Error("No active server");
    const res = await fetch(
      `${base}/api/v1/server/get-public-key?server_id=${encodeURIComponent(serverId)}`,
      { credentials: "include" },
    );
    if (!res.ok) throw new Error(`Failed to fetch public key: ${res.status}`);
    const result = (await res.json()) as { data?: { public_key?: string } };
    const key = result.data?.public_key;
    if (!key) throw new Error("Server returned no public key");
    return key;
  }, [serverId]);

  const listRegistries = useCallback(
    () =>
      dispatch<{ registries: Registry[] | null }>("/api/v1/registry/list", {}).then(
        (p) => p.registries ?? [],
      ),
    [dispatch],
  );

  const createRegistry = useCallback(
    async (address: string, username: string, password: string) => {
      // Registry passwords are secrets — encrypt with the server key.
      const key = await getPublicKey();
      const enc = await encryptSecret(password, key);
      await dispatch("/api/v1/registry/create", {
        method: "POST",
        body: { address, username, password: enc, encrypted: true },
      });
    },
    [dispatch, getPublicKey],
  );

  const deleteRegistry = useCallback(
    (address: string) =>
      dispatch("/api/v1/registry/delete", {
        method: "POST",
        body: { address },
      }),
    [dispatch],
  );

  const listNetworks = useCallback(
    () =>
      dispatch<{ networks: Network[] | null }>("/api/v1/network/list", {}).then(
        (p) => p.networks ?? [],
      ),
    [dispatch],
  );

  const createNetwork = useCallback(
    (name: string, driver?: string) =>
      dispatch("/api/v1/network/create", {
        method: "POST",
        body: { name, driver: driver || undefined },
      }),
    [dispatch],
  );

  const deleteNetwork = useCallback(
    (name: string) =>
      dispatch("/api/v1/network/delete", { method: "POST", body: { name } }),
    [dispatch],
  );

  const listVolumes = useCallback(
    () =>
      dispatch<{ volumes: Volume[] | null }>("/api/v1/volume/list", {}).then(
        (p) => p.volumes ?? [],
      ),
    [dispatch],
  );

  const createVolume = useCallback(
    (name: string) =>
      dispatch("/api/v1/volume/create", { method: "POST", body: { name } }),
    [dispatch],
  );

  const deleteVolume = useCallback(
    (name: string) =>
      dispatch("/api/v1/volume/delete", { method: "POST", body: { name } }),
    [dispatch],
  );

  const listDomains = useCallback(
    () =>
      dispatch<{ domains: RegisteredDomain[] | null }>("/api/v1/domain/list", {}).then(
        (p) => p.domains ?? [],
      ),
    [dispatch],
  );

  // createDomain registers (or replaces) a name. In certificate mode the key
  // is a secret: it is encrypted with the server key, like registry passwords.
  const createDomain = useCallback(
    async (
      name: string,
      tls: DomainTLS,
      cert?: { certificate: string; key: string },
    ) => {
      const body: Record<string, string> = { name, tls };
      if (tls === "certificate" && cert) {
        const key = await getPublicKey();
        body.certificate = cert.certificate;
        body.key = await encryptSecret(cert.key, key);
      }
      await dispatch("/api/v1/domain/create", { method: "POST", body });
    },
    [dispatch, getPublicKey],
  );

  const deleteDomain = useCallback(
    (name: string) =>
      dispatch("/api/v1/domain/delete", { method: "POST", body: { name } }),
    [dispatch],
  );

  const updateAgent = useCallback(
    (version: string) =>
      dispatch("/api/v1/agent/update", { method: "POST", body: { version } }),
    [dispatch],
  );

  // Memoized so consumers can depend on the object (or its callbacks)
  // without refetching on every parent re-render.
  return useMemo(
    () => ({
      hasServer: Boolean(serverId),
      listRegistries,
      createRegistry,
      deleteRegistry,
      listNetworks,
      createNetwork,
      deleteNetwork,
      listVolumes,
      createVolume,
      deleteVolume,
      listDomains,
      createDomain,
      deleteDomain,
      updateAgent,
    }),
    [
      serverId,
      listRegistries,
      createRegistry,
      deleteRegistry,
      listNetworks,
      createNetwork,
      deleteNetwork,
      listVolumes,
      createVolume,
      deleteVolume,
      listDomains,
      createDomain,
      deleteDomain,
      updateAgent,
    ],
  );
}

export type Docker = ReturnType<typeof useDocker>;
