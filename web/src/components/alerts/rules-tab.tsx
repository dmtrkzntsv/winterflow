import { useCallback, useEffect, useState } from "react";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
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
import { RuleDialog } from "@/components/alerts/rule-dialog";
import { alertsGet, alertsPost } from "@/hooks/use-alerts";
import { routingText, ruleSummary, type Channel, type Rule } from "@/lib/alerts";
import { cn } from "@/lib/utils";

// RulesTab lists the org's rules, servers first, with an inline enable
// toggle, edit and delete.
export function RulesTab() {
  const [rules, setRules] = useState<Rule[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Rule | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<Rule | null>(null);

  const refresh = useCallback(async () => {
    try {
      const [r, c] = await Promise.all([
        alertsGet<Rule[] | null>("get-rules"),
        alertsGet<Channel[] | null>("get-channels"),
      ]);
      setRules(r ?? []);
      setChannels(c ?? []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load rules");
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const toggle = async (rule: Rule, enabled: boolean) => {
    setRules((rs) => rs.map((r) => (r.id === rule.id ? { ...r, enabled } : r)));
    try {
      await alertsPost("save-rule", { ...rule, enabled });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Update failed");
      void refresh();
    }
  };

  const remove = async () => {
    if (!deleting) return;
    try {
      await alertsPost("delete-rule", { id: deleting.id });
      toast.success("Rule deleted");
      void refresh();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(null);
    }
  };

  const group = (kind: "server" | "app", title: string) => {
    const list = rules.filter((r) => r.target_kind === kind);
    return (
      <section className="space-y-2" aria-label={title}>
        <h2 className="text-sm font-medium text-muted-foreground">{title}</h2>
        {list.length === 0 ? (
          <p className="text-sm text-muted-foreground">No rules.</p>
        ) : (
          list.map((r) => (
            <Card key={r.id} className="flex items-center gap-3 p-3.5" data-testid="rule-row">
              <Switch
                checked={r.enabled}
                aria-label={`Enable ${ruleSummary(r)}`}
                onCheckedChange={(v) => void toggle(r, v)}
              />
              <div className={cn("min-w-0 flex-1", !r.enabled && "opacity-60")}>
                <div className="truncate font-medium">{ruleSummary(r)}</div>
                <div className="truncate text-xs text-muted-foreground">{routingText(r.channel_ids, channels)}</div>
              </div>
              <Button variant="ghost" size="icon" aria-label={`Edit ${ruleSummary(r)}`} onClick={() => setEditing(r)}>
                <Pencil className="size-4" />
              </Button>
              <Button variant="ghost" size="icon" aria-label={`Delete ${ruleSummary(r)}`} onClick={() => setDeleting(r)}>
                <Trash2 className="size-4" />
              </Button>
            </Card>
          ))
        )}
      </section>
    );
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          Rules apply to every server or app; override them per server or app from its Monitoring or Metrics page.
        </p>
        <Button size="sm" onClick={() => setEditing(null)}>
          <Plus className="size-4" /> Add rule
        </Button>
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      {group("server", "Servers")}
      {group("app", "Apps")}

      {editing !== undefined ? (
        <RuleDialog
          key={editing?.id ?? "new"}
          open
          onOpenChange={(open) => !open && setEditing(undefined)}
          rule={editing ?? undefined}
          channels={channels}
          onSaved={() => void refresh()}
        />
      ) : null}

      <AlertDialog open={deleting !== null} onOpenChange={(open) => !open && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this rule?</AlertDialogTitle>
            <AlertDialogDescription>
              {deleting ? ruleSummary(deleting) : ""}. Its overrides go with it; its history stays.
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
