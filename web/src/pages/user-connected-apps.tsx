import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Plug } from "lucide-react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { RemoveButton } from "@/components/remove-button";
import { Row, RowList } from "@/components/section";
import { useConfirmTarget } from "@/hooks/use-confirm-target";
import { useNow } from "@/hooks/use-now";
import { connectedAppMeta, connectedAppWhere, createAccountApi, type ConnectedApp } from "@/lib/account";
import { ApiError } from "@/lib/api";
import { returnFocusTo } from "@/lib/focus";

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

// UserConnectedAppsPage is Settings → Account → Connected apps: the MCP
// clients (Claude, an IDE…) the user approved, each with full access to the
// organization; disconnect one after a confirmation and its tokens stop
// working at once.
export default function UserConnectedAppsPage() {
  const api = useMemo(() => createAccountApi(), []);
  const [apps, setApps] = useState<ConnectedApp[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const revokeConfirm = useConfirmTarget<ConnectedApp>();
  const opener = useRef<HTMLElement | null>(null);
  const now = useNow();

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setApps(await api.connectedApps());
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 404
          ? "Connecting apps isn't available on this Winterflow."
          : (errorText(e) ?? "Couldn't load the connected apps."),
      );
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const revoke = async (a: ConnectedApp) => {
    try {
      await api.revokeConnectedApp(a.grant_id);
      setApps((list) => list.filter((x) => x.grant_id !== a.grant_id));
      toast.success(`${a.client_name} disconnected`);
    } catch (e) {
      toast.error("Couldn't disconnect the app", { description: errorText(e) });
      throw e;
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Connected apps"
        subtitle="AI assistants and tools you let use Winterflow as you, over MCP."
      />
      {apps.length > 0 ? (
        <RowList label="Connected apps">
          {apps.map((a) => (
            <Row
              key={a.grant_id}
              title={a.client_name}
              description={connectedAppWhere(a)}
              meta={connectedAppMeta(a, now)}
              actions={
                <RemoveButton
                  label={`Disconnect ${a.client_name}`}
                  onClick={(trigger) => {
                    opener.current = trigger;
                    revokeConfirm.ask(a);
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
        <EmptyState icon={Plug} title="No connected apps. An MCP client asks here for access when you add Winterflow to it." />
      )}
      <ConfirmDialog
        open={revokeConfirm.open}
        onOpenChange={revokeConfirm.onOpenChange}
        title={`Disconnect ${revokeConfirm.target?.client_name ?? "this app"}?`}
        description="It loses access at once. To use it again, connect it again from the app."
        confirmLabel="Disconnect"
        pendingLabel="Disconnecting…"
        onCloseAutoFocus={(e) => returnFocusTo(opener)(e)}
        onConfirm={() => (revokeConfirm.target ? revoke(revokeConfirm.target) : undefined)}
      />
    </div>
  );
}
