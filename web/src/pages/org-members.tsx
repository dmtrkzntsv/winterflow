import { useCallback, useEffect, useMemo, useState } from "react";
import { Check, Copy, Pencil, Plus, Trash2, KeyRound } from "lucide-react";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { apiBaseUrl } from "@/config";
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
import { useProfile } from "@/context/use-profile";
import { AppIcon } from "@/components/app-icon";
import { IconPicker } from "@/components/icon-picker";

type Organization = {
  org_id: string;
  name: string;
  icon: string;
  color: string;
};

type Member = {
  id: string;
  name: string;
  email: string;
  role: string;
  provider: string;
  last_seen_at: string;
  created_at: string;
};

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${apiBaseUrl}${path}`, {
    credentials: "include",
    ...init,
  });
  const body = await res.json().catch(() => null);
  if (!res.ok || !body?.success) {
    throw new Error(body?.message ?? `Request failed: ${res.status}`);
  }
  return body.data as T;
}

function postJSON<T>(path: string, payload: unknown): Promise<T> {
  return api<T>(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

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
    if (!document.execCommand("copy")) throw new Error("copy rejected");
  } finally {
    ta.remove();
  }
}

// Reveal shows a just-generated temp password with a copy button — the only
// time it is visible (the server stores a bcrypt hash).
function Reveal({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-center gap-2">
      <Input
        readOnly
        value={value}
        className="font-mono text-xs"
        onFocus={(e) => e.currentTarget.select()}
      />
      <Button
        variant="outline"
        size="icon"
        onClick={() => {
          void copyText(value)
            .then(() => {
              setCopied(true);
              toast.success("Copied");
            })
            .catch(() =>
              toast.error("Copy failed — select the text and copy manually"),
            );
        }}
      >
        {copied ? <Check /> : <Copy />}
      </Button>
    </div>
  );
}

// OrgMembersPage: admins manage the organization's users — create accounts
// with one-time temp passwords, change roles, reset passwords, remove.
export default function OrgMembersPage() {
  const breadcrumbs = useMemo(
    () => [{ label: "Organization" }, { label: "Members" }],
    [],
  );
  useAppBreadcrumbs(breadcrumbs);
  const { profile, isAdmin } = useProfile();

  const [org, setOrg] = useState<Organization | null>(null);
  const [orgEditOpen, setOrgEditOpen] = useState(false);
  const [orgName, setOrgName] = useState("");
  const [orgIcon, setOrgIcon] = useState("");
  const [orgColor, setOrgColor] = useState("#64748b");
  const [orgBusy, setOrgBusy] = useState(false);

  const [members, setMembers] = useState<Member[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [addOpen, setAddOpen] = useState(false);
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("member");
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<{
    email: string;
    temp_password: string;
  } | null>(null);
  const [reset, setReset] = useState<{
    name: string;
    temp_password: string;
  } | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api<Member[] | null>("/api/v1/org/get-members");
      setMembers(data ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load members");
    } finally {
      setLoading(false);
    }
  }, []);

  const refreshOrg = useCallback(async () => {
    try {
      setOrg(await api<Organization>("/api/v1/org/get-organization"));
    } catch (e) {
      console.error("Failed to load organization", e);
    }
  }, []);

  useEffect(() => {
    void refresh();
    void refreshOrg();
  }, [refresh, refreshOrg]);

  const openOrgEdit = () => {
    setOrgName(org?.name ?? "");
    setOrgIcon(org?.icon ?? "");
    setOrgColor(org?.color || "#64748b");
    setOrgEditOpen(true);
  };

  const handleOrgSave = async () => {
    if (!orgName.trim()) {
      toast.error("Organization name is required");
      return;
    }
    setOrgBusy(true);
    try {
      await postJSON("/api/v1/org/update-organization", {
        name: orgName.trim(),
        icon: orgIcon,
        color: orgColor,
      });
      toast.success("Organization updated");
      setOrgEditOpen(false);
      void refreshOrg();
    } catch (e) {
      toast.error("Failed to update organization", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setOrgBusy(false);
    }
  };

  const openAdd = () => {
    setName("");
    setEmail("");
    setRole("member");
    setCreated(null);
    setAddOpen(true);
  };

  const handleCreate = async () => {
    if (!name.trim() || !email.trim()) {
      toast.error("Name and email are required");
      return;
    }
    setBusy(true);
    try {
      const data = await postJSON<{ email: string; temp_password: string }>(
        "/api/v1/org/create-user",
        { name: name.trim(), email: email.trim(), role },
      );
      setCreated(data);
      void refresh();
    } catch (e) {
      toast.error("Failed to create user", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const handleRole = async (m: Member, newRole: string) => {
    try {
      await postJSON("/api/v1/org/update-member", {
        user_id: m.id,
        role: newRole,
      });
      toast.success(`${m.name} is now ${newRole}`);
      void refresh();
    } catch (e) {
      toast.error("Failed to update role", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
  };

  const handleReset = async (m: Member) => {
    if (!window.confirm(`Reset ${m.name}'s password? Their current password stops working immediately.`)) {
      return;
    }
    try {
      const data = await postJSON<{ temp_password: string }>(
        "/api/v1/org/reset-member-password",
        { user_id: m.id },
      );
      setReset({ name: m.name, temp_password: data.temp_password });
    } catch (e) {
      toast.error("Failed to reset password", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
  };

  const handleRemove = async (m: Member) => {
    if (!window.confirm(`Remove ${m.name}? Their account, tokens, and access are deleted permanently.`)) {
      return;
    }
    try {
      await postJSON("/api/v1/org/remove-member", { user_id: m.id });
      toast.success("Member removed");
      void refresh();
    } catch (e) {
      toast.error("Failed to remove member", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
  };

  return (
    <div className="mx-auto w-full max-w-4xl space-y-6">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle>Organization</CardTitle>
          {isAdmin ? (
            <Button size="sm" variant="outline" onClick={openOrgEdit}>
              <Pencil /> Edit
            </Button>
          ) : null}
        </CardHeader>
        <CardContent>
          {org ? (
            <div className="flex items-center gap-3">
              <AppIcon icon={org.icon} color={org.color} />
              <div>
                <p className="font-medium">{org.name}</p>
                <p className="text-xs text-muted-foreground">{org.org_id}</p>
              </div>
            </div>
          ) : (
            <Spinner />
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle>Members</CardTitle>
          <Button size="sm" onClick={openAdd}>
            <Plus /> Add user
          </Button>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="flex min-h-32 items-center justify-center">
              <Spinner />
            </div>
          ) : error ? (
            <p className="text-sm text-destructive">{error}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Email</TableHead>
                  <TableHead>Role</TableHead>
                  <TableHead>Provider</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {members.map((m) => {
                  const isSelf = m.id === profile?.user_id;
                  return (
                    <TableRow key={m.id}>
                      <TableCell className="font-medium">
                        {m.name}
                        {isSelf ? (
                          <span className="text-muted-foreground"> (you)</span>
                        ) : null}
                      </TableCell>
                      <TableCell>{m.email || "—"}</TableCell>
                      <TableCell>
                        {m.role === "owner" || isSelf ? (
                          <span className="capitalize">{m.role}</span>
                        ) : (
                          <select
                            className="bg-transparent text-sm"
                            value={m.role}
                            onChange={(e) => void handleRole(m, e.target.value)}
                          >
                            <option value="admin">admin</option>
                            <option value="member">member</option>
                          </select>
                        )}
                      </TableCell>
                      <TableCell>{m.provider || "—"}</TableCell>
                      <TableCell className="text-right">
                        {!isSelf && m.email ? (
                          <Button
                            variant="ghost"
                            size="icon"
                            title="Reset password"
                            onClick={() => void handleReset(m)}
                          >
                            <KeyRound />
                          </Button>
                        ) : null}
                        {!isSelf ? (
                          <Button
                            variant="ghost"
                            size="icon"
                            title="Remove"
                            onClick={() => void handleRemove(m)}
                          >
                            <Trash2 className="text-destructive" />
                          </Button>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent>
          {created ? (
            <>
              <DialogHeader>
                <DialogTitle>User created</DialogTitle>
                <DialogDescription>
                  Share these credentials with {created.email}. The temporary
                  password is shown only once; they must change it on first
                  login.
                </DialogDescription>
              </DialogHeader>
              <Reveal value={created.temp_password} />
              <DialogFooter>
                <Button onClick={() => setAddOpen(false)}>Done</Button>
              </DialogFooter>
            </>
          ) : (
            <>
              <DialogHeader>
                <DialogTitle>Add user</DialogTitle>
                <DialogDescription>
                  Creates an account in your organization with a temporary
                  password.
                </DialogDescription>
              </DialogHeader>
              <div className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="member-name">Name</Label>
                  <Input
                    id="member-name"
                    value={name}
                    maxLength={64}
                    onChange={(e) => setName(e.target.value)}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="member-email">Email</Label>
                  <Input
                    id="member-email"
                    type="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Role</Label>
                  <div className="flex gap-2">
                    {["member", "admin"].map((r) => (
                      <Button
                        key={r}
                        type="button"
                        size="sm"
                        variant={role === r ? "default" : "outline"}
                        onClick={() => setRole(r)}
                      >
                        {r}
                      </Button>
                    ))}
                  </div>
                </div>
              </div>
              <DialogFooter>
                <Button
                  variant="outline"
                  onClick={() => setAddOpen(false)}
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

      <Dialog open={orgEditOpen} onOpenChange={setOrgEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit organization</DialogTitle>
            <DialogDescription>
              Name, icon, and color — same vocabulary as apps.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="flex items-end gap-3">
              <div className="space-y-2">
                <Label>Icon</Label>
                <IconPicker
                  value={orgIcon}
                  color={orgColor}
                  onChange={setOrgIcon}
                />
              </div>
              <div className="flex-1 space-y-2">
                <Label htmlFor="org-name">Name</Label>
                <Input
                  id="org-name"
                  value={orgName}
                  maxLength={64}
                  onChange={(e) => setOrgName(e.target.value)}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="org-color">Color</Label>
                <Input
                  id="org-color"
                  type="color"
                  className="h-9 w-16 p-1"
                  value={orgColor}
                  onChange={(e) => setOrgColor(e.target.value)}
                />
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setOrgEditOpen(false)}
              disabled={orgBusy}
            >
              Cancel
            </Button>
            <Button onClick={() => void handleOrgSave()} disabled={orgBusy}>
              {orgBusy ? "Saving…" : "Save"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={reset !== null} onOpenChange={(o) => !o && setReset(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Password reset</DialogTitle>
            <DialogDescription>
              New temporary password for {reset?.name} — shown only once; they
              must change it on first login.
            </DialogDescription>
          </DialogHeader>
          {reset ? <Reveal value={reset.temp_password} /> : null}
          <DialogFooter>
            <Button onClick={() => setReset(null)}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
