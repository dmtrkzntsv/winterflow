import { useEffect, useState, type FormEvent } from "react";
import { useParams } from "react-router-dom";
import { CheckCircle2, KeyRound } from "lucide-react";

import { AuthHeading, AuthScreen } from "@/components/auth-screen";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { PasswordInput } from "@/components/ui/password-input";
import { Spinner } from "@/components/ui/spinner";
import { apiBase } from "@/config";
import { ApiError, createJsonClient, jsonPost } from "@/lib/api";
import { encryptSecret } from "@/lib/ecies";

// The view /api/v1/secret-request/view answers.
type RequestView = {
  server_id: string;
  server_name: string;
  app_id: string;
  app_name: string;
  names: string[];
  notes: Record<string, string>;
  message: string;
  expires_at: string;
  public_key: string;
};

type Phase =
  | { kind: "loading" }
  | { kind: "gone" }
  | { kind: "failed"; message: string }
  | { kind: "ask"; v: RequestView }
  | { kind: "done" };

const client = createJsonClient(apiBase);

// GONE is the one message for a link that can't be used: unknown, another
// organization's, submitted, used or expired all read the same.
const GONE = "This link can't be used. It may have expired or been used already. Ask your agent for a new one.";

// SecretRequestPage is /secrets/:token: a coding agent asked the user for
// an app's secret values. The browser encrypts each value to the server's
// key before posting it, so neither the agent nor the API ever sees one.
// RequireAuth signs the person in first and comes back here.
export default function SecretRequestPage() {
  const { token = "" } = useParams();
  const [phase, setPhase] = useState<Phase>({ kind: "loading" });
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    client.call<RequestView>(`/api/v1/secret-request/view?${new URLSearchParams({ token })}`).then(
      (v) => {
        if (!cancelled) setPhase({ kind: "ask", v });
      },
      (e: unknown) => {
        if (cancelled) return;
        if (e instanceof ApiError && e.status === 404) setPhase({ kind: "gone" });
        else setPhase({ kind: "failed", message: e instanceof Error ? e.message : "This link can't be read." });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [token]);

  if (phase.kind === "loading") {
    return (
      <AuthScreen terms={false}>
        <div className="flex min-h-64 items-center justify-center" aria-busy="true">
          <Spinner />
        </div>
      </AuthScreen>
    );
  }

  if (phase.kind === "gone" || phase.kind === "failed") {
    return (
      <AuthScreen terms={false}>
        <div className="flex min-h-64 flex-col justify-center gap-6">
          <AuthHeading title="Link not available" subtitle="Nothing to fill in here." />
          <Alert variant="destructive" role="alert">
            <AlertDescription>{phase.kind === "gone" ? GONE : phase.message}</AlertDescription>
          </Alert>
        </div>
      </AuthScreen>
    );
  }

  if (phase.kind === "done") {
    return (
      <AuthScreen terms={false}>
        <div className="flex min-h-64 flex-col items-center justify-center gap-4 text-center">
          <CheckCircle2 className="size-10 text-emerald-600" aria-hidden />
          <h1 className="text-2xl font-semibold tracking-tight">Submitted — your agent can continue</h1>
          <p className="text-muted-foreground text-sm">The values were encrypted in this browser. You can close this tab.</p>
        </div>
      </AuthScreen>
    );
  }

  const { v } = phase;
  const missing = v.names.filter((n) => !values[n]);
  const where = v.app_name || v.app_id ? `${v.app_name || v.app_id} on ${v.server_name || v.server_id}` : `a new app on ${v.server_name || v.server_id}`;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (missing.length > 0 || busy) return;
    if (!v.public_key) {
      setError("This server's public key isn't available yet, so the values can't be encrypted. Try again once its agent is online.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const encrypted: Record<string, string> = {};
      for (const name of v.names) encrypted[name] = await encryptSecret(values[name], v.public_key);
      await client.call(`/api/v1/secret-request/submit`, jsonPost({ token, values: encrypted }));
      setValues({});
      setPhase({ kind: "done" });
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setPhase({ kind: "gone" });
        return;
      }
      setError(err instanceof Error ? err.message : "That didn't work. Try again.");
      setBusy(false);
    }
  };

  return (
    <AuthScreen terms={false}>
      <form className="flex flex-col gap-6" onSubmit={(e) => void submit(e)}>
        <AuthHeading title="Your agent asks for secrets" subtitle={<>For {where}.</>} />
        {v.message ? (
          <div className="flex gap-3 rounded-lg border p-4 text-sm">
            <KeyRound className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
            <p className="whitespace-pre-wrap break-words" data-testid="secret-request-message">
              {v.message}
            </p>
          </div>
        ) : null}
        <div className="flex flex-col gap-4">
          {v.names.map((name) => (
            <div key={name} className="flex flex-col gap-1.5">
              <Label htmlFor={`secret-${name}`} className="font-mono">
                {name}
              </Label>
              <PasswordInput
                id={`secret-${name}`}
                name={name}
                autoComplete="off"
                required
                disabled={busy}
                value={values[name] ?? ""}
                onChange={(e) => setValues((prev) => ({ ...prev, [name]: e.target.value }))}
              />
              {v.notes[name] ? (
                <p className="text-muted-foreground whitespace-pre-wrap break-words text-xs">{v.notes[name]}</p>
              ) : null}
            </div>
          ))}
        </div>
        <p className="text-muted-foreground text-xs">
          Each value is encrypted in this browser with the server's key. Neither the agent nor Winterflow can read it.
        </p>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
        <Button type="submit" className="min-h-10" disabled={busy || missing.length > 0}>
          {busy ? "Encrypting…" : "Submit"}
        </Button>
      </form>
    </AuthScreen>
  );
}
