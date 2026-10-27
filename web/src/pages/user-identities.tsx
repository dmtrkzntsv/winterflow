import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { ProviderIcon } from "@/components/provider-icon";
import { Row, RowList, Section } from "@/components/section";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { apiBase } from "@/config";
import { useConfirmTarget } from "@/hooks/use-confirm-target";
import { useNow } from "@/hooks/use-now";
import { createAccountApi, providerName, type Identity } from "@/lib/account";
import { loadAuthState, type SignInProvider } from "@/lib/auth-state";
import { returnFocusTo } from "@/lib/focus";
import { settingsPath } from "@/lib/routes";
import { providerStartURL, signInErrorText } from "@/lib/sign-in";
import { relativeTime } from "@/lib/utils";

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

// UserIdentitiesPage is Settings → Account → Sign-in methods: the provider
// accounts (Google, Apple, single sign-on) linked to this one, a Link
// button for each configured provider not linked yet (the API's link flow
// comes back here, with ?error=<code> when it failed), and Unlink (the
// API refuses to unlink the account's only way in).
export default function UserIdentitiesPage() {
  const api = useMemo(() => createAccountApi(), []);
  const [search, setSearch] = useSearchParams();
  const [identities, setIdentities] = useState<Identity[]>([]);
  const [providers, setProviders] = useState<SignInProvider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const unlinkConfirm = useConfirmTarget<Identity>();
  const opener = useRef<HTMLElement | null>(null);
  const now = useNow();
  const linkError = search.get("error");

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setIdentities(await api.identities());
    } catch (e) {
      setError(errorText(e) ?? "Couldn't load your sign-in methods.");
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    let cancelled = false;
    loadAuthState().then(
      (s) => {
        if (!cancelled) setProviders(s.providers);
      },
      () => {},
    );
    return () => {
      cancelled = true;
    };
  }, []);

  const unlink = async (i: Identity) => {
    try {
      await api.unlinkIdentity(i.provider);
      setIdentities((list) => list.filter((x) => x.provider !== i.provider));
      toast.success(`${providerName(i.provider, providers)} unlinked`);
    } catch (e) {
      toast.error("Couldn't unlink the account", { description: errorText(e) });
      throw e;
    }
  };

  const linked = new Set(identities.map((i) => i.provider));
  const linkable = loading ? [] : providers.filter((p) => !linked.has(p.id));
  const here = settingsPath("identities");

  return (
    <div className="space-y-6">
      <PageHeader title="Sign-in methods" subtitle="Accounts you can sign in to Winterflow with." />
      {linkError ? (
        <Alert variant="destructive" role="alert" className="max-w-xl">
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>{signInErrorText(linkError)}</span>
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                const next = new URLSearchParams(search);
                next.delete("error");
                setSearch(next, { replace: true });
              }}
            >
              Dismiss
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}
      <Section title="Linked accounts">
        {identities.length > 0 ? (
          <RowList label="Linked accounts">
            {identities.map((i) => {
              const name = providerName(i.provider, providers);
              return (
                <Row
                  key={i.provider}
                  leading={<ProviderIcon id={i.provider} />}
                  title={name}
                  description={i.email || undefined}
                  meta={`Linked ${relativeTime(new Date(i.created_at), now)}`}
                  actions={
                    <Button
                      size="sm"
                      variant="ghost"
                      className="min-h-10 sm:min-h-8"
                      onClick={(e) => {
                        opener.current = e.currentTarget;
                        unlinkConfirm.ask(i);
                      }}
                    >
                      Unlink
                    </Button>
                  }
                />
              );
            })}
          </RowList>
        ) : loading ? (
          <ListPlaceholder loading failed="" />
        ) : error ? (
          <ListPlaceholder loading={false} failed={error} onRetry={() => void refresh()} />
        ) : (
          <p className="text-sm text-muted-foreground">No linked accounts. You sign in with your email and password.</p>
        )}
      </Section>
      {linkable.length > 0 ? (
        <Section title="Link an account" description="Then you can sign in with it too.">
          <div className="flex flex-wrap gap-2">
            {linkable.map((p) => (
              <Button key={p.id} asChild variant="outline" className="min-h-10">
                <a href={providerStartURL(apiBase, p.id, "link", here)}>
                  <ProviderIcon id={p.id} />
                  Link {p.name}
                </a>
              </Button>
            ))}
          </div>
        </Section>
      ) : null}
      <ConfirmDialog
        open={unlinkConfirm.open}
        onOpenChange={unlinkConfirm.onOpenChange}
        title={`Unlink ${unlinkConfirm.target ? providerName(unlinkConfirm.target.provider, providers) : "this account"}?`}
        description="You won't be able to sign in with it any more."
        confirmLabel="Unlink"
        pendingLabel="Unlinking…"
        onCloseAutoFocus={(e) => returnFocusTo(opener)(e)}
        onConfirm={() => (unlinkConfirm.target ? unlink(unlinkConfirm.target) : undefined)}
      />
    </div>
  );
}
