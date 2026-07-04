import { useMemo, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { apiBaseUrl } from "@/config";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { PasswordInput } from "@/components/ui/password-input";
import { useProfile } from "@/context/use-profile";

// UserPasswordPage changes the local account password. Users flagged
// must_change_password are forced here by the layout guard until they set
// their own password.
export default function UserPasswordPage() {
  const breadcrumbs = useMemo(
    () => [{ label: "User" }, { label: "Change password" }],
    [],
  );
  useAppBreadcrumbs(breadcrumbs);
  const navigate = useNavigate();
  const { profile, refresh } = useProfile();
  const [busy, setBusy] = useState(false);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const current = String(form.get("current") ?? "");
    const next = String(form.get("next") ?? "");
    const confirm = String(form.get("confirm") ?? "");
    if (next.length < 4) {
      toast.error("New password must be at least 4 characters");
      return;
    }
    if (next !== confirm) {
      toast.error("Passwords do not match");
      return;
    }
    setBusy(true);
    try {
      const res = await fetch(`${apiBaseUrl}/api/v1/user/change-password`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ current_password: current, new_password: next }),
      });
      const body = await res.json().catch(() => null);
      if (!res.ok || !body?.success) {
        throw new Error(body?.message ?? `Request failed: ${res.status}`);
      }
      toast.success("Password changed");
      await refresh();
      navigate("/");
    } catch (e) {
      toast.error("Failed to change password", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mx-auto w-full max-w-md space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Change password</CardTitle>
        </CardHeader>
        <CardContent>
          {profile?.must_change_password ? (
            <p className="mb-4 text-sm text-muted-foreground">
              You&apos;re using a temporary password — set your own to
              continue.
            </p>
          ) : null}
          <form className="space-y-4" onSubmit={(e) => void handleSubmit(e)}>
            <div className="space-y-2">
              <Label htmlFor="current">Current password</Label>
              <PasswordInput
                id="current"
                name="current"
                required
                toggleLabel="Show password"
                hideLabel="Hide password"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="next">New password</Label>
              <PasswordInput
                id="next"
                name="next"
                required
                toggleLabel="Show password"
                hideLabel="Hide password"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="confirm">Confirm new password</Label>
              <PasswordInput
                id="confirm"
                name="confirm"
                required
                toggleLabel="Show password"
                hideLabel="Hide password"
              />
            </div>
            <Button type="submit" className="w-full" disabled={busy}>
              {busy ? "Saving…" : "Change password"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
