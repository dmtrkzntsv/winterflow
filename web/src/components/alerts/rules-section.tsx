import { useCallback, useEffect, useRef, useState } from "react";
import { Plus } from "lucide-react";
import { toast } from "sonner";

import { RuleDialog } from "@/components/alerts/rule-dialog";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList, Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { alertsGet, alertsPost } from "@/hooks/use-alerts";
import { routingText, ruleSummary, type Channel, type Rule, type TargetKind } from "@/lib/alerts";
import { returnFocusTo } from "@/lib/focus";
import { cn } from "@/lib/utils";

// RulesSection is Notifications → Alert rules: the org's rules, servers
// first, each with an inline on/off switch and Edit/Delete in its menu.
// channels come from the page (shared with Channels), so each rule's
// "notify" line names current channels.
export function RulesSection({ channels }: { channels: Channel[] }) {
  const [rules, setRules] = useState<Rule[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Listing again is a bump of attempt: the effect below does the work.
  const [attempt, setAttempt] = useState(0);
  // editing: undefined = closed, null = a new rule.
  const [editing, setEditing] = useState<Rule | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<Rule | null>(null);
  // The dialogs have no trigger of their own: focus goes back to what
  // opened them, or to Add rule when that left with its row.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);

  const refresh = useCallback(() => setAttempt((n) => n + 1), []);

  useEffect(() => {
    let live = true;
    alertsGet<Rule[] | null>("get-rules").then(
      (list) => {
        if (!live) return;
        setRules(list ?? []);
        setError(null);
      },
      (e) => {
        if (live) setError(e instanceof Error ? e.message : "Couldn't load the rules");
      },
    );
    return () => {
      live = false;
    };
  }, [attempt]);

  const toggle = async (rule: Rule, enabled: boolean) => {
    setRules((rs) => rs && rs.map((r) => (r.id === rule.id ? { ...r, enabled } : r)));
    try {
      await alertsPost("save-rule", { ...rule, enabled });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Update failed");
      refresh();
    }
  };

  const remove = async (rule: Rule) => {
    try {
      await alertsPost("delete-rule", { id: rule.id });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Couldn't delete the rule", {
        description: "The rule is still there.",
      });
      throw e;
    }
    toast.success("Rule deleted");
    // The row goes before the confirmation closes, so focus moves on.
    setRules((rs) => rs && rs.filter((r) => r.id !== rule.id));
    refresh();
  };

  const group = (kind: TargetKind, title: string) => {
    const list = (rules ?? []).filter((r) => r.target_kind === kind);
    return (
      <section className="space-y-2" aria-label={title}>
        <h3 className="px-1 text-xs font-medium text-muted-foreground">{title}</h3>
        {list.length === 0 ? (
          <p className="px-1 text-sm text-muted-foreground">No rules for {kind === "app" ? "apps" : "servers"} yet.</p>
        ) : (
          <RowList label={`${title} rules`}>
            {list.map((r) => (
              <Row
                key={r.id}
                testId="rule-row"
                leading={
                  <Switch
                    className="relative max-sm:after:absolute max-sm:after:-inset-x-1 max-sm:after:-inset-y-2.5"
                    checked={r.enabled}
                    aria-label={`Enable ${ruleSummary(r)}`}
                    onCheckedChange={(v) => void toggle(r, v)}
                  />
                }
                title={<span className={cn(!r.enabled && "opacity-60")}>{ruleSummary(r)}</span>}
                description={routingText(r.channel_ids, channels)}
                actions={
                  <RowMenu
                    label={`Actions for ${ruleSummary(r)}`}
                    items={[
                      {
                        label: "Edit",
                        onSelect: (trigger) => {
                          opener.current = trigger;
                          setEditing(r);
                        },
                      },
                      {
                        label: "Delete",
                        destructive: true,
                        onSelect: (trigger) => {
                          opener.current = trigger;
                          setDeleting(r);
                        },
                      },
                    ]}
                  />
                }
              />
            ))}
          </RowList>
        )}
      </section>
    );
  };

  return (
    <Section
      title="Alert rules"
      description="When an alert fires. A server or app can adjust a rule from its Monitoring or Metrics page."
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
          <Plus className="size-4" /> Add rule
        </Button>
      }
    >
      {error ? (
        <div className="flex flex-wrap items-center gap-3">
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
          <Button size="sm" variant="outline" className="max-sm:h-10" onClick={refresh}>
            Try again
          </Button>
        </div>
      ) : null}
      {rules !== null ? (
        <div className="space-y-4">
          {group("server", "Servers")}
          {group("app", "Apps")}
        </div>
      ) : !error ? (
        <div className="flex h-16 items-center justify-center">
          <Spinner />
        </div>
      ) : null}

      {editing !== undefined ? (
        <RuleDialog
          key={editing?.id ?? "new"}
          open
          onOpenChange={(open) => !open && setEditing(undefined)}
          rule={editing ?? undefined}
          channels={channels}
          returnFocus={opener}
          onSaved={refresh}
        />
      ) : null}

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="Delete this rule?"
        description={`${deleting ? ruleSummary(deleting) : ""}. Its adjustments go with it; its alerts stay in the Inbox.`}
        confirmLabel="Delete rule"
        pendingLabel="Deleting…"
        onCloseAutoFocus={(e) => returnFocusTo(opener, addButton)(e)}
        onConfirm={() => (deleting ? remove(deleting) : undefined)}
      />
    </Section>
  );
}
