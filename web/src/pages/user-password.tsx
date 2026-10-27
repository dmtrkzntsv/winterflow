import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { PasswordInput } from "@/components/ui/password-input";
import { apiBaseUrl } from "@/config";
import { useProfile } from "@/context/use-profile";
import { apiFetch, readApiError } from "@/lib/api";

type Problem = { field: "next" | "confirm" | "form"; message: string };

// UserPasswordPage is Settings → Account → Password: change the local
// account's password. Users flagged must_change_password are sent here by
// the layout until they choose their own. Problems show under the field
// they belong to.
export default function UserPasswordPage() {
  const navigate = useNavigate();
  const { profile, refresh } = useProfile();
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const current = String(form.get("current") ?? "");
    const next = String(form.get("next") ?? "");
    const confirm = String(form.get("confirm") ?? "");
    if (next.length < 4) {
      setProblem({ field: "next", message: "Use at least 4 characters." });
      return;
    }
    if (next !== confirm) {
      setProblem({ field: "confirm", message: "The two new passwords don't match." });
      return;
    }
    setProblem(null);
    setBusy(true);
    try {
      const res = await apiFetch(`${apiBaseUrl}/api/v1/user/change-password`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ current_password: current, new_password: next }),
      });
      if (!res.ok) throw await readApiError(res);
      toast.success("Password changed");
      await refresh();
      navigate("/");
    } catch (e) {
      setProblem({ field: "form", message: e instanceof Error ? e.message : "Couldn't change the password." });
    } finally {
      setBusy(false);
    }
  };

  const field = (name: "current" | "next" | "confirm", label: string) => {
    const invalid = problem?.field === name;
    return (
      <div className="space-y-2">
        <Label htmlFor={name}>{label}</Label>
        <PasswordInput
          id={name}
          name={name}
          // Password managers fill the current one and offer to save the new.
          autoComplete={name === "current" ? "current-password" : "new-password"}
          required
          aria-invalid={invalid || undefined}
          aria-describedby={invalid ? `${name}-error` : undefined}
          toggleLabel="Show password"
          hideLabel="Hide password"
        />
        {invalid ? (
          <p id={`${name}-error`} role="alert" className="text-xs text-destructive">
            {problem?.message}
          </p>
        ) : null}
      </div>
    );
  };

  return (
    <div className="space-y-6">
      <PageHeader title="Password" subtitle="The password you sign in with." />
      {profile?.must_change_password ? (
        <Alert role="status" className="max-w-sm">
          <AlertDescription>You're using a temporary password: choose your own to continue.</AlertDescription>
        </Alert>
      ) : null}
      <form className="max-w-sm space-y-4" onSubmit={(e) => void handleSubmit(e)}>
        {field("current", "Current password")}
        {field("next", "New password")}
        {field("confirm", "Confirm new password")}
        {problem?.field === "form" ? (
          <p role="alert" className="text-sm text-destructive">
            {problem.message}
          </p>
        ) : null}
        <Button type="submit" disabled={busy}>
          {busy ? "Saving…" : "Change password"}
        </Button>
      </form>
    </div>
  );
}
