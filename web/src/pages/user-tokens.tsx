import { useMemo, useState } from "react";
import { Check, Copy, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { useTokens, type CreatedToken } from "@/hooks/use-tokens";

const EXPIRY_OPTIONS = [
  { label: "30 days", days: 30 },
  { label: "90 days", days: 90 },
  { label: "1 year", days: 365 },
  { label: "No expiry", days: 0 },
];

function fmtDate(iso: string | null): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleDateString();
}

// copyText works on insecure (plain-http LAN) origins too, where browsers
// don't expose navigator.clipboard — same constraint as crypto.subtle in
// lib/ecies.ts. Falls back to the legacy selection-based copy.
async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  try {
    if (!document.execCommand("copy")) {
      throw new Error("copy rejected");
    }
  } finally {
    ta.remove();
  }
}

// UserTokensPage manages personal access tokens: list, generate (with a
// one-time plaintext reveal), and revoke. Tokens authenticate API calls via
// "Authorization: Bearer wfp_…" or Basic auth (token as password).
export default function UserTokensPage() {
  const breadcrumbs = useMemo(
    () => [{ label: "User" }, { label: "API tokens" }],
    [],
  );
  useAppBreadcrumbs(breadcrumbs);

  const { tokens, loading, error, create, remove } = useTokens();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [name, setName] = useState("");
  const [expiryDays, setExpiryDays] = useState(30);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<CreatedToken | null>(null);
  const [copied, setCopied] = useState(false);

  const openDialog = () => {
    setName("");
    setExpiryDays(30);
    setCreated(null);
    setCopied(false);
    setDialogOpen(true);
  };

  const handleCreate = async () => {
    if (!name.trim()) {
      toast.error("Give the token a name");
      return;
    }
    setBusy(true);
    try {
      setCreated(await create(name.trim(), expiryDays));
    } catch (e) {
      toast.error("Failed to create token", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const handleCopy = async () => {
    if (!created) return;
    try {
      await copyText(created.token);
      setCopied(true);
      toast.success("Token copied");
    } catch {
      toast.error("Copy failed — select the token and copy it manually");
    }
  };

  const handleRevoke = async (tokenId: string, tokenName: string) => {
    if (
      !window.confirm(
        `Revoke "${tokenName}"? Clients using it will stop working immediately.`,
      )
    ) {
      return;
    }
    try {
      await remove(tokenId);
      toast.success("Token revoked");
    } catch (e) {
      toast.error("Failed to revoke token", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
  };

  return (
    <div className="mx-auto w-full max-w-4xl space-y-6">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle>Personal access tokens</CardTitle>
          <Button size="sm" onClick={openDialog}>
            <Plus /> Generate token
          </Button>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="flex min-h-32 items-center justify-center">
              <Spinner />
            </div>
          ) : error ? (
            <p className="text-sm text-destructive">{error}</p>
          ) : tokens.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No tokens yet. Generate one to call the API from scripts or CI —
              send it as <code>Authorization: Bearer wfp_…</code>.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Token</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead>Last used</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {tokens.map((t) => (
                  <TableRow key={t.token_id}>
                    <TableCell className="font-medium">{t.name}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {t.prefix}…
                    </TableCell>
                    <TableCell>{fmtDate(t.created_at)}</TableCell>
                    <TableCell>{fmtDate(t.expires_at)}</TableCell>
                    <TableCell>
                      {t.last_used_at ? fmtDate(t.last_used_at) : "Never"}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => void handleRevoke(t.token_id, t.name)}
                      >
                        <Trash2 className="text-destructive" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          {created ? (
            <>
              <DialogHeader>
                <DialogTitle>Token created</DialogTitle>
                <DialogDescription>
                  Copy it now — you won&apos;t be able to see it again.
                </DialogDescription>
              </DialogHeader>
              <div className="flex items-center gap-2">
                <Input
                  readOnly
                  value={created.token}
                  className="font-mono text-xs"
                  onFocus={(e) => e.currentTarget.select()}
                />
                <Button
                  variant="outline"
                  size="icon"
                  onClick={() => void handleCopy()}
                >
                  {copied ? <Check /> : <Copy />}
                </Button>
              </div>
              <DialogFooter>
                <Button onClick={() => setDialogOpen(false)}>Done</Button>
              </DialogFooter>
            </>
          ) : (
            <>
              <DialogHeader>
                <DialogTitle>Generate token</DialogTitle>
                <DialogDescription>
                  The token has the same access as your account. No scopes.
                </DialogDescription>
              </DialogHeader>
              <div className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="token-name">Name</Label>
                  <Input
                    id="token-name"
                    placeholder="e.g. CI deploy"
                    value={name}
                    maxLength={64}
                    onChange={(e) => setName(e.target.value)}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Expires</Label>
                  <div className="flex gap-2">
                    {EXPIRY_OPTIONS.map((o) => (
                      <Button
                        key={o.days}
                        type="button"
                        size="sm"
                        variant={expiryDays === o.days ? "default" : "outline"}
                        onClick={() => setExpiryDays(o.days)}
                      >
                        {o.label}
                      </Button>
                    ))}
                  </div>
                </div>
              </div>
              <DialogFooter>
                <Button
                  variant="outline"
                  onClick={() => setDialogOpen(false)}
                  disabled={busy}
                >
                  Cancel
                </Button>
                <Button onClick={() => void handleCreate()} disabled={busy}>
                  {busy ? "Creating…" : "Create"}
                </Button>
              </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
