import { useCallback, useEffect, useState } from "react";

import { apiBaseUrl } from "@/config";

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
  const res = await fetch(`${apiBaseUrl}${path}`, {
    credentials: "include",
    ...init,
  });
  const body = await res.json().catch(() => null);
  if (!res.ok || !body?.success) {
    throw new Error(body?.message ?? `Request failed: ${res.status}`);
  }
  return body.data as T;
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
      void refresh();
    },
    [refresh],
  );

  return { tokens, loading, error, refresh, create, remove };
}
