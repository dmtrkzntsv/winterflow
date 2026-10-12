import { useCallback, useEffect, useState } from "react";
import { BellRing, Pencil, Plus, Send, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { ChannelDialog } from "@/components/alerts/channel-dialog";
import { alertsGet, alertsPost } from "@/hooks/use-alerts";
import { channelSummary, type Channel } from "@/lib/alerts";

// ChannelsTab lists the org's notification channels: destination, default
// flag, last delivery, with Test, Edit and Delete.
export function ChannelsTab() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // editing: undefined = closed, null = new channel.
  const [editing, setEditing] = useState<Channel | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<Channel | null>(null);
  const [testing, setTesting] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setChannels((await alertsGet<Channel[] | null>("get-channels")) ?? []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load channels");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const test = async (ch: Channel) => {
    setTesting(ch.id);
    try {
      const res = await alertsPost<{ ok: boolean; error: string }>("test-channel", { id: ch.id });
      if (res.ok) toast.success(`Test sent to ${ch.name}`);
      else toast.error(`${ch.name}: ${res.error}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Test failed");
    } finally {
      setTesting(null);
      void refresh();
    }
  };

  const remove = async () => {
    if (!deleting) return;
    try {
      await alertsPost("delete-channel", { id: deleting.id });
      toast.success(`Deleted ${deleting.name}`);
      void refresh();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(null);
    }
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          Telegram, Discord, Slack, ntfy, Gotify, Pushover, email or any webhook.
        </p>
        <Button size="sm" onClick={() => setEditing(null)}>
          <Plus className="size-4" /> Add channel
        </Button>
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      {!loading && channels.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <BellRing />
            </EmptyMedia>
            <EmptyTitle>No channels yet</EmptyTitle>
            <EmptyDescription>Alerts are recorded in History; add a channel to get notified.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <div className="grid gap-2">
          {channels.map((ch) => (
            <Card key={ch.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 p-3.5" data-testid="channel-row">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="truncate font-medium">{ch.name}</span>
                  {ch.is_default ? <Badge variant="secondary">Default</Badge> : null}
                  {!ch.enabled ? <Badge variant="outline">Disabled</Badge> : null}
                </div>
                <div className="truncate text-xs text-muted-foreground">{channelSummary(ch)}</div>
                {ch.last_error ? (
                  <div className="truncate text-xs text-destructive" title={ch.last_error}>
                    Last delivery failed: {ch.last_error}
                  </div>
                ) : ch.last_sent_at ? (
                  <div className="text-xs text-muted-foreground">
                    Last sent {new Date(ch.last_sent_at).toLocaleString()}
                  </div>
                ) : null}
              </div>
              <div className="flex gap-1">
                <Button variant="ghost" size="sm" onClick={() => void test(ch)} disabled={testing === ch.id}>
                  <Send className="size-4" /> Test
                </Button>
                <Button variant="ghost" size="icon" aria-label={`Edit ${ch.name}`} onClick={() => setEditing(ch)}>
                  <Pencil className="size-4" />
                </Button>
                <Button variant="ghost" size="icon" aria-label={`Delete ${ch.name}`} onClick={() => setDeleting(ch)}>
                  <Trash2 className="size-4" />
                </Button>
              </div>
            </Card>
          ))}
        </div>
      )}

      {editing !== undefined ? (
        <ChannelDialog
          key={editing?.id ?? "new"}
          open
          onOpenChange={(open) => !open && setEditing(undefined)}
          channel={editing ?? undefined}
          onSaved={() => void refresh()}
        />
      ) : null}

      <AlertDialog open={deleting !== null} onOpenChange={(open) => !open && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {deleting?.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Rules that send to it will skip it. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={() => void remove()}>Delete</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
