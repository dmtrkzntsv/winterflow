import { useRef, useState } from "react";
import { KeyRound, Plus } from "lucide-react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyField } from "@/components/copy-field";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { RemoveButton } from "@/components/remove-button";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Row, RowList } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useNow } from "@/hooks/use-now";
import { useTokens, type ApiToken, type CreatedToken } from "@/hooks/use-tokens";
import { returnFocusTo } from "@/lib/focus";
import { tokenMeta } from "@/lib/tokens";
import { cn } from "@/lib/utils";

const EXPIRY_OPTIONS = [
  { label: "30 days", days: 30 },
  { label: "90 days", days: 90 },
  { label: "1 year", days: 365 },
  { label: "No expiry", days: 0 },
];

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

// GenerateTokenDialog names a new token and its expiry, then shows the
// token once (only its hash is kept) with Copy. The page mounts it per
// opening, so the token does not outlive the dialog.
function GenerateTokenDialog({
  onOpenChange,
  create,
}: {
  onOpenChange: (open: boolean) => void;
  create: (name: string, days: number) => Promise<CreatedToken>;
}) {
  const [name, setName] = useState("");
  const [days, setDays] = useState(30);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<CreatedToken | null>(null);

  const generate = async () => {
    if (!name.trim()) {
      setError("Give the token a name.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      setCreated(await create(name.trim(), days));
    } catch (e) {
      setError(errorText(e) ?? "Couldn't generate the token.");
    } finally {
      setBusy(false);
    }
  };

  if (created) {
    return (
      <ResponsiveDialog
        open
        onOpenChange={onOpenChange}
        title="Token generated"
        description="Copy it now: you won't be able to see it again."
        footer={
          <Button className="max-sm:h-10" onClick={() => onOpenChange(false)}>
            Done
          </Button>
        }
      >
        <CopyField value={created.token} label="Token" />
      </ResponsiveDialog>
    );
  }

  return (
    <ResponsiveDialog
      open
      onOpenChange={onOpenChange}
      title="Generate token"
      description="It can do everything your account can."
      footer={
        <>
          <Button type="button" variant="outline" className="max-sm:h-10" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="submit" form="token-form" className="max-sm:h-10" disabled={busy}>
            {busy ? "Generating…" : "Generate"}
          </Button>
        </>
      }
    >
      <form
        id="token-form"
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void generate();
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="token-name">Name</Label>
          <Input
            id="token-name"
            className="max-sm:h-10"
            placeholder="e.g. CI deploy"
            value={name}
            maxLength={64}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className="space-y-2">
          <Label id="token-expiry-label">Expires</Label>
          <div role="radiogroup" aria-labelledby="token-expiry-label" className="flex flex-wrap gap-2">
            {EXPIRY_OPTIONS.map((o) => (
              <button
                key={o.days}
                type="button"
                role="radio"
                aria-checked={days === o.days}
                onClick={() => setDays(o.days)}
                className={cn(
                  "min-h-10 rounded-md border px-3 text-sm transition-colors duration-150 hover:border-primary/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:min-h-8",
                  days === o.days && "border-primary bg-primary/5 font-medium",
                )}
              >
                {o.label}
              </button>
            ))}
          </div>
        </div>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </form>
    </ResponsiveDialog>
  );
}

// UserTokensPage is Settings → Account → API tokens: personal access
// tokens for scripts and CI (sent as "Authorization: Bearer wfp_…", or as
// the Basic-auth password); generate one, or revoke one after a
// confirmation.
export default function UserTokensPage() {
  const { tokens, loading, error, create, remove, refresh } = useTokens();
  const [generating, setGenerating] = useState(false);
  const [toRevoke, setToRevoke] = useState<ApiToken | null>(null);
  // The confirmation has no trigger of its own: focus goes back to the
  // row's button, or to Generate token once that row is gone.
  const opener = useRef<HTMLElement | null>(null);
  const generateButton = useRef<HTMLButtonElement | null>(null);
  const now = useNow();

  const revoke = async (t: ApiToken) => {
    try {
      await remove(t.token_id);
      toast.success("Token revoked");
    } catch (e) {
      toast.error("Couldn't revoke the token", { description: errorText(e) });
      throw e;
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="API tokens"
        subtitle={
          <>
            Let scripts and CI call the API as you: send a token as{" "}
            <code className="font-mono">Authorization: Bearer wfp_…</code>.
          </>
        }
        actions={
          <Button ref={generateButton} className="max-sm:h-10" onClick={() => setGenerating(true)}>
            <Plus className="size-4" /> Generate token
          </Button>
        }
      />
      {tokens.length > 0 ? (
        <RowList label="API tokens">
          {tokens.map((t) => (
            <Row
              key={t.token_id}
              title={t.name}
              description={<span className="font-mono">{t.prefix}…</span>}
              meta={tokenMeta(t, now)}
              actions={
                <RemoveButton
                  label={`Revoke ${t.name}`}
                  onClick={(trigger) => {
                    opener.current = trigger;
                    setToRevoke(t);
                  }}
                />
              }
            />
          ))}
        </RowList>
      ) : loading ? (
        <ListPlaceholder loading failed="" />
      ) : error ? (
        <ListPlaceholder loading={false} failed={error} onRetry={() => void refresh()} />
      ) : (
        <EmptyState icon={KeyRound} title="No tokens yet. Generate one to call the API from a script." />
      )}
      {generating ? <GenerateTokenDialog onOpenChange={setGenerating} create={create} /> : null}
      <ConfirmDialog
        open={toRevoke !== null}
        onOpenChange={(o) => !o && setToRevoke(null)}
        title={`Revoke ${toRevoke?.name ?? "this token"}?`}
        description="Anything that uses it stops working at once."
        confirmLabel="Revoke token"
        pendingLabel="Revoking…"
        onCloseAutoFocus={(e) => returnFocusTo(opener, generateButton)(e)}
        onConfirm={() => (toRevoke ? revoke(toRevoke) : undefined)}
      />
    </div>
  );
}
