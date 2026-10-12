import { useCallback, useMemo, useRef } from "react";

import { useNotifications } from "@/context/use-notifications";
import { useServers } from "@/context/use-servers";
import { readApiError } from "@/lib/api";
import { ENCRYPTED, encryptSecret } from "@/lib/ecies";
import type { Domain, DomainTLS } from "@/lib/domains";
import {
  storageBody,
  type Storage,
  type StorageDraft,
  type StorageSaved,
  type StorageTest,
} from "@/lib/storages";
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

// DomainCreated is domain.create's result: the stored registration (to
// splice into a list) and the proxy reload's warnings.
export type DomainCreated = { domain: Domain; warnings: string[] };

type Accepted = { data?: { request_id?: string } };

// fetchPublicKey reads a server's ECIES public key.
async function fetchPublicKey(serverId: string): Promise<string> {
  const res = await fetch(
    `${base}/api/v1/server/get-public-key?server_id=${encodeURIComponent(serverId)}`,
    { credentials: "include" },
  );
  if (!res.ok) throw new Error(`Failed to fetch public key: ${res.status}`);
  const result = (await res.json()) as { data?: { public_key?: string } };
  const key = result.data?.public_key;
  if (!key) throw new Error("Server returned no public key");
  return key;
}

// How long to wait for a storage command's result. storage.save may run up
// to 8 minutes on the agent (the probe, docker ps, then redeploys of the
// apps using the storage); storage.test up to 30 seconds. Waiting less
// reports "Timed out" for a save that then succeeds.
export const STORAGE_SAVE_TIMEOUT_MS = 9 * 60_000;
export const STORAGE_TEST_TIMEOUT_MS = 2 * 60_000;

// useDocker exposes registry/network/volume operations. Each dispatches an agent
// command and awaits its SSE result (the agent's Docker daemon is the source of
// truth, so even list reads round-trip).
export function useDocker() {
  const { activeServerId } = useServers();
  const { waitFor } = useNotifications();

  const serverId = activeServerId;

  // dispatch sends a request, awaits its SSE result (for timeoutMs, else the
  // notifications default), and returns the payload.
  const dispatch = useCallback(
    async <T>(
      path: string,
      opts: { method?: string; body?: unknown; query?: boolean; timeoutMs?: number },
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
      // The API explains a refusal (e.g. an invalid domain) in `message`.
      if (!res.ok) throw await readApiError(res);
      const accepted = (await res.json()) as Accepted;
      const ref = accepted.data?.request_id;
      if (!ref) throw new Error("No request id");
      const result = await waitFor(ref, opts.timeoutMs);
      if (result.status && result.status !== 0) {
        throw new Error(result.error || "Operation failed");
      }
      return result.payload as T;
    },
    [serverId, waitFor],
  );

  // The server's public key is fetched once per server (a storage is often
  // tested, then saved); a failed fetch is not kept.
  const publicKey = useRef<{ serverId: string; key: Promise<string> } | null>(null);
  const getPublicKey = useCallback((): Promise<string> => {
    if (!serverId) return Promise.reject(new Error("No active server"));
    if (publicKey.current?.serverId !== serverId) {
      const key = fetchPublicKey(serverId);
      publicKey.current = { serverId, key };
      key.catch(() => {
        if (publicKey.current?.key === key) publicKey.current = null;
      });
    }
    return publicKey.current.key;
  }, [serverId]);

  // encrypt encrypts a secret (a registry password, a certificate key, a
  // storage's secret key) for the active server.
  const encrypt = useCallback(
    async (plaintext: string) => encryptSecret(plaintext, await getPublicKey()),
    [getPublicKey],
  );

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
      await dispatch("/api/v1/registry/create", {
        method: "POST",
        body: { address, username, password: await encrypt(password), encrypted: true },
      });
    },
    [dispatch, encrypt],
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
      dispatch<{ domains: Domain[] | null }>("/api/v1/domain/list", {}).then(
        (p) => p.domains ?? [],
      ),
    [dispatch],
  );

  // createDomain registers (or replaces) a name. In certificate mode the key
  // is a secret: it is encrypted with the server key, like registry passwords.
  // It resolves with the stored registration and the proxy reload's warnings
  // (the change itself applied).
  const createDomain = useCallback(
    async (
      name: string,
      tls: DomainTLS,
      cert?: { certificate: string; key: string },
    ): Promise<DomainCreated> => {
      const body: Record<string, string> = { name, tls };
      if (tls === "certificate" && cert) {
        body.certificate = cert.certificate;
        body.key = await encrypt(cert.key);
      }
      const res = await dispatch<{ domain: Domain; warnings?: string[] | null }>(
        "/api/v1/domain/create",
        { method: "POST", body },
      );
      return { domain: res.domain, warnings: res.warnings ?? [] };
    },
    [dispatch, encrypt],
  );

  // deleteDomain resolves with the proxy reload's warnings.
  const deleteDomain = useCallback(
    async (name: string): Promise<string[]> => {
      const res = await dispatch<{ warnings?: string[] } | null>("/api/v1/domain/delete", {
        method: "POST",
        body: { name },
      });
      return res?.warnings ?? [];
    },
    [dispatch],
  );

  const listStorages = useCallback(
    () =>
      dispatch<{ storages: Storage[] | null }>("/api/v1/storage/list", {}).then(
        (p) => p.storages ?? [],
      ),
    [dispatch],
  );

  // storageRequest is the body for a draft. A typed secret is encrypted with
  // the server key, like registry passwords; an empty one keeps the stored
  // secret ("<encrypted>"). Local folders have no secret.
  const storageRequest = useCallback(
    async (draft: StorageDraft) => {
      if (draft.type !== "s3") return storageBody(draft, "");
      return storageBody(draft, draft.secret ? await encrypt(draft.secret) : ENCRYPTED);
    },
    [encrypt],
  );

  // testStorage resolves with the probe's outcome: a failed probe is
  // { ok: false, error }, not a rejection.
  const testStorage = useCallback(
    async (draft: StorageDraft): Promise<StorageTest> =>
      dispatch<StorageTest>("/api/v1/storage/test", {
        method: "POST",
        body: await storageRequest(draft),
        timeoutMs: STORAGE_TEST_TIMEOUT_MS,
      }),
    [dispatch, storageRequest],
  );

  // saveStorage adds (create) or edits a storage and resolves with the
  // stored storage, the apps redeployed with it and the redeploy warnings
  // (the storage itself was saved). The agent refuses an add over an
  // existing name, and an edit of a missing one.
  const saveStorage = useCallback(
    async (draft: StorageDraft, create: boolean): Promise<StorageSaved> => {
      const res = await dispatch<{
        storage: Storage;
        redeployed?: string[] | null;
        warnings?: string[] | null;
      }>("/api/v1/storage/save", {
        method: "POST",
        body: { ...(await storageRequest(draft)), create },
        timeoutMs: STORAGE_SAVE_TIMEOUT_MS,
      });
      return { storage: res.storage, redeployed: res.redeployed ?? [], warnings: res.warnings ?? [] };
    },
    [dispatch, storageRequest],
  );

  const deleteStorage = useCallback(
    async (name: string): Promise<void> => {
      await dispatch("/api/v1/storage/delete", { method: "POST", body: { name } });
    },
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
      listStorages,
      testStorage,
      saveStorage,
      deleteStorage,
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
      listStorages,
      testStorage,
      saveStorage,
      deleteStorage,
      updateAgent,
    ],
  );
}

export type Docker = ReturnType<typeof useDocker>;
