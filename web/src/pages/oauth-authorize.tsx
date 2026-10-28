import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { ShieldCheck } from "lucide-react";

import { AuthHeading, AuthScreen } from "@/components/auth-screen";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { apiBase } from "@/config";
import { ApiError, createJsonClient, jsonPost } from "@/lib/api";
import { leaveTo } from "@/lib/leave";

// The OAuth parameters /oauth/authorize carries; the consent routes take
// the same names, as a query (describe) and as a body (approve, decline).
const PARAMS = [
  "client_id",
  "redirect_uri",
  "response_type",
  "code_challenge",
  "code_challenge_method",
  "resource",
  "scope",
  "state",
] as const;

type Description = {
  client_name: string;
  redirect_host: string;
  is_loopback: boolean;
  error_redirect_url: string;
};

type Phase =
  | { kind: "loading" }
  | { kind: "failed"; message: string }
  | { kind: "ask"; d: Description }
  | { kind: "leaving" };

const client = createJsonClient(apiBase);

// OAuthAuthorizePage is /oauth/authorize, the consent screen of an MCP
// client (Claude, an IDE…) asking for access: which app, where its answer
// goes, what it gets (everything the account can do in its organization),
// then Approve or Decline, after which the browser goes back to the app.
// RequireAuth signs the person in first and comes back here.
export default function OAuthAuthorizePage() {
  const [search] = useSearchParams();
  const request = useMemo(() => {
    const out: Record<string, string> = {};
    for (const k of PARAMS) {
      const v = search.get(k);
      if (v !== null) out[k] = v;
    }
    return out;
  }, [search]);
  const [phase, setPhase] = useState<Phase>({ kind: "loading" });
  const [busy, setBusy] = useState<"approve" | "decline" | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const q = new URLSearchParams(request);
    client.call<Description>(`/api/v1/authserver/get-authorization-request?${q}`).then(
      (d) => {
        if (cancelled) return;
        if (d.error_redirect_url) {
          // A bad request whose redirect is trusted: the app hears why.
          setPhase({ kind: "leaving" });
          leaveTo(d.error_redirect_url);
          return;
        }
        setPhase({ kind: "ask", d });
      },
      (e: unknown) => {
        if (cancelled) return;
        const message =
          e instanceof ApiError && e.status === 404
            ? "Connecting apps isn't available on this Winterflow."
            : e instanceof Error
              ? e.message
              : "This request can't be read.";
        setPhase({ kind: "failed", message });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [request]);

  const decide = async (decision: "approve" | "decline") => {
    setBusy(decision);
    setActionError(null);
    try {
      const { redirect_url } = await client.call<{ redirect_url: string }>(
        `/api/v1/authserver/${decision}-authorization`,
        jsonPost(request),
      );
      setPhase({ kind: "leaving" });
      leaveTo(redirect_url);
    } catch (e) {
      setActionError(e instanceof Error ? e.message : "That didn't work. Try again.");
      setBusy(null);
    }
  };

  if (phase.kind === "loading" || phase.kind === "leaving") {
    return (
      <AuthScreen terms={false}>
        <div className="flex min-h-64 items-center justify-center" aria-busy="true">
          <Spinner />
        </div>
      </AuthScreen>
    );
  }

  if (phase.kind === "failed") {
    return (
      <AuthScreen terms={false}>
        <div className="flex min-h-64 flex-col justify-center gap-6">
          <AuthHeading title="Can't connect this app" subtitle="The app's request was refused." />
          <Alert variant="destructive" role="alert">
            <AlertDescription>{phase.message}</AlertDescription>
          </Alert>
        </div>
      </AuthScreen>
    );
  }

  const { d } = phase;
  return (
    <AuthScreen terms={false}>
      <div className="flex flex-col gap-6">
        <AuthHeading
          title={`Connect ${d.client_name}?`}
          subtitle={
            d.is_loopback ? (
              <>It runs on this computer ({d.redirect_host}).</>
            ) : (
              <>
                You'll be sent back to <span className="font-medium text-foreground">{d.redirect_host}</span>.
              </>
            )
          }
        />
        <div className="flex gap-3 rounded-lg border p-4 text-sm">
          <ShieldCheck className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
          <div className="space-y-1">
            <p className="font-medium">Full access to your organization's servers and apps</p>
            <p className="text-muted-foreground">
              It can do everything you can. Disconnect it any time in Settings → Connected apps.
            </p>
          </div>
        </div>
        {actionError ? (
          <p role="alert" className="text-sm text-destructive">
            {actionError}
          </p>
        ) : null}
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button variant="outline" className="min-h-10" disabled={busy !== null} onClick={() => void decide("decline")}>
            {busy === "decline" ? "Declining…" : "Decline"}
          </Button>
          <Button className="min-h-10" disabled={busy !== null} onClick={() => void decide("approve")}>
            {busy === "approve" ? "Approving…" : "Approve"}
          </Button>
        </div>
      </div>
    </AuthScreen>
  );
}
