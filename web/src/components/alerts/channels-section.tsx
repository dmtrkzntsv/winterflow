import { useRef, useState } from "react";
import { BellRing, Plus } from "lucide-react";
import { toast } from "sonner";

import { ChannelDialog } from "@/components/alerts/channel-dialog";
import { ServiceIcon } from "@/components/alerts/service-icon";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList, Section } from "@/components/section";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { alertsPost } from "@/hooks/use-alerts";
import { useNow } from "@/hooks/use-now";
import { channelService, channelSummary, type Channel } from "@/lib/alerts";
import { returnFocusTo } from "@/lib/focus";
import { relativeTime } from "@/lib/utils";

// ChannelsSection is Notifications → Channels: where alerts are sent, each
// with its service, default and on/off state and last delivery; Send test,
// Edit and Delete in its menu. channels come from the page (shared with
// the rules); onChanged lists them again.
export function ChannelsSection({
  channels,
  loading,
  error,
  onChanged,
}: {
  channels: Channel[];
  loading: boolean;
  error: string | null;
  onChanged: () => Promise<void> | void;
}) {
  // editing: undefined = closed, null = a new channel.
  const [editing, setEditing] = useState<Channel | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<Channel | null>(null);
  const [testing, setTesting] = useState<string | null>(null);
  // The dialogs have no trigger of their own: focus goes back to what
  // opened them, or to Add channel when that left with its row.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);
  const now = useNow();

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
      void onChanged();
    }
  };

  const remove = async (ch: Channel) => {
    try {
      await alertsPost("delete-channel", { id: ch.id });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Couldn't delete the channel", {
        description: `${ch.name} is still there.`,
      });
      throw e;
    }
    toast.success(`Deleted ${ch.name}`);
    // The row is gone before the confirmation closes, so focus moves on.
    await onChanged();
  };

  return (
    <Section
      title="Channels"
      description="Where alerts are sent: a chat app, email, or any webhook."
      actions={
        <Button
          ref={addButton}
          size="sm"
          variant="outline"
          className="max-sm:h-10"
          onClick={(e) => {
            opener.current = e.currentTarget;
            setEditing(null);
          }}
        >
          <Plus className="size-4" /> Add channel
        </Button>
      }
    >
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
      {channels.length > 0 ? (
        <RowList label="Channels">
          {channels.map((ch) => (
            <Row
              key={ch.id}
              testId="channel-row"
              leading={
                <span className="flex size-9 items-center justify-center rounded-lg bg-muted">
                  <ServiceIcon service={channelService(ch)} className="size-4 text-muted-foreground" />
                </span>
              }
              title={
                <span className="flex items-center gap-2">
                  <span className="truncate">{ch.name}</span>
                  {ch.is_default ? <Badge variant="secondary">Default</Badge> : null}
                  {!ch.enabled ? <Badge variant="outline">Off</Badge> : null}
                </span>
              }
              description={channelSummary(ch)}
              meta={
                ch.last_error ? (
                  <span className="line-clamp-1 max-w-64 text-status-bad" title={ch.last_error}>
                    Last delivery failed: {ch.last_error}
                  </span>
                ) : ch.last_sent_at ? (
                  `Last sent ${relativeTime(new Date(ch.last_sent_at), now)}`
                ) : (
                  "Nothing sent yet"
                )
              }
              actions={
                <RowMenu
                  label={`Actions for ${ch.name}`}
                  items={[
                    {
                      label: testing === ch.id ? "Sending test…" : "Send test",
                      disabled: testing === ch.id,
                      onSelect: () => void test(ch),
                    },
                    {
                      label: "Edit",
                      onSelect: (trigger) => {
                        opener.current = trigger;
                        setEditing(ch);
                      },
                    },
                    {
                      label: "Delete",
                      destructive: true,
                      onSelect: (trigger) => {
                        opener.current = trigger;
                        setDeleting(ch);
                      },
                    },
                  ]}
                />
              }
            />
          ))}
        </RowList>
      ) : loading ? (
        <ListPlaceholder loading failed="" />
      ) : !error ? (
        <EmptyState icon={BellRing} title="No channels yet: alerts only show in the Inbox until you add one." />
      ) : null}

      {editing !== undefined ? (
        <ChannelDialog
          key={editing?.id ?? "new"}
          open
          onOpenChange={(open) => !open && setEditing(undefined)}
          channel={editing ?? undefined}
          returnFocus={opener}
          onSaved={() => void onChanged()}
        />
      ) : null}

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={`Delete ${deleting?.name ?? "this channel"}?`}
        description="Rules that send to it skip it from now on. This can't be undone."
        confirmLabel="Delete channel"
        pendingLabel="Deleting…"
        onCloseAutoFocus={(e) => returnFocusTo(opener, addButton)(e)}
        onConfirm={() => (deleting ? remove(deleting) : undefined)}
      />
    </Section>
  );
}
