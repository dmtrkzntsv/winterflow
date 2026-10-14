import { useCallback, useEffect, useRef, useState } from "react";
import { Plus } from "lucide-react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyField } from "@/components/copy-field";
import { IconPicker } from "@/components/icon-picker";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList } from "@/components/section";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { apiBaseUrl } from "@/config";
import { useProfile } from "@/context/use-profile";
import { useConfirmTarget } from "@/hooks/use-confirm-target";
import { readApiError } from "@/lib/api";
import { returnFocusTo } from "@/lib/focus";
import { initials } from "@/lib/members";

type Organization = { org_id: string; name: string; icon: string; color: string };

type Member = {
  id: string;
  name: string;
  email: string;
  provider: string;
  last_seen_at: string;
  created_at: string;
};

type TempPassword = { name: string; temp_password: string };

const DEFAULT_COLOR = "#64748b";

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${apiBaseUrl}${path}`, { credentials: "include", ...init });
  if (!res.ok) throw await readApiError(res);
  return ((await res.json()) as { data: T }).data;
}

function postJSON<T>(path: string, payload: unknown): Promise<T> {
  return api<T>(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

// OrganizationSettings is Settings → Organization → General: the name,
// icon and colour everyone in the organization sees, edited in place.
function OrganizationSettings() {
  const [org, setOrg] = useState<Organization | null>(null);
  const [failed, setFailed] = useState(false);
  const [name, setName] = useState("");
  const [icon, setIcon] = useState("");
  const [color, setColor] = useState(DEFAULT_COLOR);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setFailed(false);
    try {
      const o = await api<Organization>("/api/v1/org/get-organization");
      setOrg(o);
      setName(o.name);
      setIcon(o.icon);
      setColor(o.color || DEFAULT_COLOR);
    } catch {
      setFailed(true);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const trimmed = name.trim();
  const dirty = org !== null && (trimmed !== org.name || icon !== org.icon || color !== (org.color || DEFAULT_COLOR));

  const save = async () => {
    if (!org) return;
    setBusy(true);
    try {
      await postJSON("/api/v1/org/update-organization", { name: trimmed, icon, color });
      toast.success("Organization saved");
      setOrg({ ...org, name: trimmed, icon, color });
    } catch (e) {
      toast.error("Couldn't save the organization", { description: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader title="Organization" subtitle="Its name, icon and colour, as everyone in it sees them." />
      {org === null ? (
        <ListPlaceholder loading={!failed} failed="Couldn't load the organization." onRetry={() => void load()} />
      ) : (
        <form
          className="max-w-lg space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (dirty && trimmed && !busy) void save();
          }}
        >
          <div className="flex flex-wrap items-end gap-3">
            <div className="space-y-2">
              <Label>Icon</Label>
              <IconPicker value={icon} name={name} color={color} onChange={setIcon} />
            </div>
            <div className="min-w-0 flex-1 basis-48 space-y-2">
              <Label htmlFor="org-name">Name</Label>
              <Input
                id="org-name"
                value={name}
                maxLength={64}
                aria-invalid={!trimmed || undefined}
                aria-describedby={!trimmed ? "org-name-error" : undefined}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="org-color">Colour</Label>
              <Input
                id="org-color"
                type="color"
                className="h-9 w-16 p-1 max-sm:h-10"
                value={color}
                onChange={(e) => setColor(e.target.value)}
              />
            </div>
          </div>
          {!trimmed ? (
            <p id="org-name-error" role="alert" className="text-xs text-destructive">
              Give the organization a name.
            </p>
          ) : null}
          <Button type="submit" disabled={!dirty || !trimmed || busy}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </form>
      )}
    </div>
  );
}

// AddMemberDialog creates an account with a one-time temporary password,
// then shows that password (the only time it is visible: the server keeps
// a bcrypt hash).
function AddMemberDialog({
  open,
  onOpenChange,
  onAdded,
  onCloseAutoFocus,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: () => void;
  onCloseAutoFocus: (event: Event) => void;
}) {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<{ email: string; temp_password: string } | null>(null);

  // Nothing (the temporary password least of all) outlives the dialog.
  const changeOpen = (next: boolean) => {
    if (!next) {
      setName("");
      setEmail("");
      setError("");
      setCreated(null);
    }
    onOpenChange(next);
  };

  const create = async () => {
    if (!name.trim() || !email.trim()) {
      setError("Enter a name and an email.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      setCreated(
        await postJSON<{ email: string; temp_password: string }>("/api/v1/org/create-user", {
          name: name.trim(),
          email: email.trim(),
        }),
      );
      onAdded();
    } catch (e) {
      setError(errorText(e) ?? "Couldn't add the member.");
    } finally {
      setBusy(false);
    }
  };

  if (created) {
    return (
      <ResponsiveDialog
        open={open}
        onOpenChange={changeOpen}
        onCloseAutoFocus={onCloseAutoFocus}
        title="Member added"
        description={`Give ${created.email} this temporary password. It's shown only once; they choose their own when they first sign in.`}
        footer={
          <Button onClick={() => changeOpen(false)}>
            Done
          </Button>
        }
      >
        <CopyField value={created.temp_password} label="Temporary password" />
      </ResponsiveDialog>
    );
  }

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      onCloseAutoFocus={onCloseAutoFocus}
      title="Add member"
      description="Creates an account in this organization with a temporary password."
      footer={
        <>
          <Button type="button" variant="outline" disabled={busy} onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form="member-form" disabled={busy}>
            {busy ? "Adding…" : "Add"}
          </Button>
        </>
      }
    >
      <form
        id="member-form"
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void create();
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="member-name">Name</Label>
          <Input id="member-name" value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="space-y-2">
          <Label htmlFor="member-email">Email</Label>
          <Input id="member-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
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

// MembersSettings is Settings → Organization → Members: everyone in the
// organization (there are no roles: any member manages the others), with
// Reset password and Remove for everyone but you, and Add member.
function MembersSettings() {
  const { profile } = useProfile();
  const [members, setMembers] = useState<Member[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false);
  const resetConfirm = useConfirmTarget<Member>();
  const removeConfirm = useConfirmTarget<Member>();
  const [reset, setReset] = useState<TempPassword | null>(null);
  // The dialogs open from a row's menu, which leaves with its row on a
  // removal: focus then goes to Add member instead of falling to <body>.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      setMembers((await api<Member[] | null>("/api/v1/org/get-members")) ?? []);
    } catch (e) {
      toast.error("Couldn't load the members", { description: errorText(e) });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const resetPassword = async (m: Member) => {
    try {
      const data = await postJSON<{ temp_password: string }>("/api/v1/org/reset-member-password", { user_id: m.id });
      // Opened before the confirmation closes (it closes once this returns):
      // the password dialog's focus trap holds focus through the
      // confirmation's own focus return, and its close sends focus back to
      // the row's menu, or to Add member.
      setReset({ name: m.name, temp_password: data.temp_password });
    } catch (e) {
      toast.error("Couldn't reset the password", { description: errorText(e) });
      throw e;
    }
  };

  const remove = async (m: Member) => {
    try {
      await postJSON("/api/v1/org/remove-member", { user_id: m.id });
      toast.success(`${m.name} removed`);
      // The answer says what changed: patch the list rather than list again.
      setMembers((prev) => prev && prev.filter((x) => x.id !== m.id));
    } catch (e) {
      toast.error("Couldn't remove the member", { description: errorText(e) });
      throw e;
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Members"
        subtitle="Everyone here can do everything in the organization."
        actions={
          <Button ref={addButton} onClick={() => setAdding(true)}>
            <Plus className="size-4" /> Add member
          </Button>
        }
      />
      {members === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load the members." onRetry={() => void refresh()} />
      ) : (
        <RowList label="Members">
          {members.map((m) => {
            const isSelf = m.id === profile?.user_id;
            return (
              <Row
                key={m.id}
                leading={
                  <Avatar className="size-9">
                    <AvatarFallback className="text-xs">{initials(m.name, m.email)}</AvatarFallback>
                  </Avatar>
                }
                title={
                  <>
                    {m.name}
                    {isSelf ? <span className="font-normal text-muted-foreground"> (you)</span> : null}
                  </>
                }
                description={m.email || `${m.provider || "External"} account`}
                actions={
                  isSelf ? null : (
                    <RowMenu
                      label={`Actions for ${m.name}`}
                      items={[
                        m.email
                          ? {
                              label: "Reset password",
                              onSelect: (trigger) => {
                                opener.current = trigger;
                                resetConfirm.ask(m);
                              },
                            }
                          : null,
                        {
                          label: "Remove",
                          destructive: true,
                          onSelect: (trigger) => {
                            opener.current = trigger;
                            removeConfirm.ask(m);
                          },
                        },
                      ]}
                    />
                  )
                }
              />
            );
          })}
        </RowList>
      )}
      <AddMemberDialog
        open={adding}
        onOpenChange={setAdding}
        onAdded={() => void refresh()}
        onCloseAutoFocus={returnFocusTo(addButton)}
      />
      <ConfirmDialog
        open={resetConfirm.open}
        onOpenChange={resetConfirm.onOpenChange}
        title={`Reset ${resetConfirm.target?.name ?? "this member"}'s password?`}
        description="Their current password stops working at once. You get a temporary password to give them; they choose their own when they next sign in."
        confirmLabel="Reset password"
        pendingLabel="Resetting…"
        onCloseAutoFocus={returnFocusTo(opener, addButton)}
        onConfirm={() => (resetConfirm.target ? resetPassword(resetConfirm.target) : undefined)}
      />
      <ConfirmDialog
        open={removeConfirm.open}
        onOpenChange={removeConfirm.onOpenChange}
        title={`Remove ${removeConfirm.target?.name ?? "this member"}?`}
        description="Their account, API tokens and access are deleted for good."
        confirmLabel="Remove member"
        pendingLabel="Removing…"
        onCloseAutoFocus={returnFocusTo(opener, addButton)}
        onConfirm={() => (removeConfirm.target ? remove(removeConfirm.target) : undefined)}
      />
      <ResponsiveDialog
        open={reset !== null}
        onOpenChange={(o) => !o && setReset(null)}
        title="Password reset"
        description={`A temporary password for ${reset?.name ?? ""}. It's shown only once; they choose their own when they next sign in.`}
        footer={
          <Button onClick={() => setReset(null)}>Done</Button>
        }
        onCloseAutoFocus={returnFocusTo(opener, addButton)}
      >
        {reset ? <CopyField value={reset.temp_password} label="Temporary password" /> : null}
      </ResponsiveDialog>
    </div>
  );
}

// OrgMembersPage is Settings → Organization → General (section
// "organization") or Members (section "members").
export default function OrgMembersPage({ section }: { section: "organization" | "members" }) {
  return section === "organization" ? <OrganizationSettings /> : <MembersSettings />;
}
