import { useCallback, useEffect, useState } from "react";

import { apiBaseUrl } from "@/config";
import { apiFetch, readApiError } from "@/lib/api";

export type ApiToken = {
  token_id: string;
  name: string;
  prefix: string;
  expires_at: string | null;
  last_used_at: string | null;
  created_at: string;
};

export type CreatedToken = {
  token_id: string;
  token: string; // plaintext — shown once, never retrievable again
  prefix: string;
  name: string;
  expires_at: string | null;
};

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await apiFetch(`${apiBaseUrl}${path}`, {
    credentials: "include",
    ...init,
  });
  if (!res.ok) throw await readApiError(res);
  const body = (await res.json()) as { data: T };
  return body.data;
}

// useTokens loads and mutates the user's personal access tokens. All three
// endpoints are synchronous (plain DB), so there is no SSE involvement.
export function useTokens() {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api<ApiToken[] | null>("/api/v1/user/get-tokens");
      setTokens(data ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load tokens");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const create = useCallback(
    async (name: string, expiresInDays: number): Promise<CreatedToken> => {
      const created = await api<CreatedToken>("/api/v1/user/create-token", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, expires_in_days: expiresInDays }),
      });
      void refresh();
      return created;
    },
    [refresh],
  );

  const remove = useCallback(
    async (tokenId: string) => {
      await api("/api/v1/user/delete-token", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token_id: tokenId }),
      });
      // The token is gone whatever the list says next: drop it here, so a
      // failed listing can't leave its row behind.
      setTokens((list) => list.filter((t) => t.token_id !== tokenId));
      // Resolve once the list is read again (focus moves on then).
      await refresh();
    },
    [refresh],
  );

  return { tokens, loading, error, refresh, create, remove };
}
