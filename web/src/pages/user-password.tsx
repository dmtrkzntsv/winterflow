import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { PasswordInput } from "@/components/ui/password-input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { apiBaseUrl } from "@/config";
import { useProfile } from "@/context/use-profile";
import { createAccountApi } from "@/lib/account";
import { apiFetch, readApiError } from "@/lib/api";

type Field = "current" | "next" | "confirm";
type Problem = { field: Field | "form"; message: string };

// UserPasswordPage is Settings → Account → Password: change the local
// account's password, or, for an account made through a sign-in provider
// (no password yet, so the profile carries no email), set a first one, so
// the account can still be reached when the provider can't. Users flagged
// must_change_password are sent here by the layout until they choose their
// own. Problems show under the field they belong to.
export default function UserPasswordPage() {
  const { profile } = useProfile();
  if (!profile) return null;
  return profile.email ? <ChangePassword /> : <SetPassword />;
}

// newPasswordProblem checks the new password and its confirmation.
function newPasswordProblem(next: string, confirm: string): Problem | null {
  if (next.length < 4) return { field: "next", message: "Use at least 4 characters." };
  if (next !== confirm) return { field: "confirm", message: "The two new passwords don't match." };
  return null;
}

async function post(path: string, body: unknown) {
  const res = await apiFetch(`${apiBaseUrl}${path}`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw await readApiError(res);
}

function PasswordField({ name, label, problem }: { name: Field; label: string; problem: Problem | null }) {
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
}

function FormProblem({ problem }: { problem: Problem | null }) {
  return problem?.field === "form" ? (
    <p role="alert" className="text-sm text-destructive">
      {problem.message}
    </p>
  ) : null;
}

function ChangePassword() {
  const navigate = useNavigate();
  const { profile, refresh } = useProfile();
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const current = String(form.get("current") ?? "");
    const next = String(form.get("next") ?? "");
    const wrong = newPasswordProblem(next, String(form.get("confirm") ?? ""));
    setProblem(wrong);
    if (wrong) return;
    setBusy(true);
    try {
      await post("/api/v1/user/change-password", { current_password: current, new_password: next });
      toast.success("Password changed");
      await refresh();
      navigate("/");
    } catch (e) {
      setProblem({ field: "form", message: e instanceof Error ? e.message : "Couldn't change the password." });
    } finally {
      setBusy(false);
    }
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
        <PasswordField name="current" label="Current password" problem={problem} />
        <PasswordField name="next" label="New password" problem={problem} />
        <PasswordField name="confirm" label="Confirm new password" problem={problem} />
        <FormProblem problem={problem} />
        <Button type="submit" disabled={busy}>
          {busy ? "Saving…" : "Change password"}
        </Button>
      </form>
    </div>
  );
}

// SetPassword gives a provider-made account its first password, for an
// address one of its linked sign-in accounts vouched for.
function SetPassword() {
  const navigate = useNavigate();
  const { refresh } = useProfile();
  const api = useMemo(() => createAccountApi(), []);
  const [emails, setEmails] = useState<string[] | null>(null);
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);

  useEffect(() => {
    let cancelled = false;
    api.identities().then(
      (list) => {
        if (cancelled) return;
        const found = [...new Set(list.map((i) => i.email.trim().toLowerCase()).filter(Boolean))];
        setEmails(found);
        setEmail(found[0] ?? "");
      },
      (e: unknown) => {
        if (cancelled) return;
        setEmails([]);
        setProblem({ field: "form", message: e instanceof Error ? e.message : "Couldn't load your sign-in accounts." });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [api]);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const next = String(form.get("next") ?? "");
    const wrong = newPasswordProblem(next, String(form.get("confirm") ?? ""));
    setProblem(wrong);
    if (wrong) return;
    setBusy(true);
    try {
      await post("/api/v1/user/set-password", { email, new_password: next });
      toast.success("Password set");
      await refresh();
      navigate("/");
    } catch (e) {
      setProblem({ field: "form", message: e instanceof Error ? e.message : "Couldn't set the password." });
    } finally {
      setBusy(false);
    }
  };

  const noEmail = emails !== null && emails.length === 0;
  return (
    <div className="space-y-6">
      <PageHeader
        title="Set a password"
        subtitle="You sign in through a provider. A password lets you sign in with your email address too, should that provider ever stop working."
      />
      <form className="max-w-sm space-y-4" onSubmit={(e) => void handleSubmit(e)}>
        {emails && emails.length > 1 ? (
          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">Email address</legend>
            <RadioGroup value={email} onValueChange={setEmail}>
              {emails.map((e) => (
                <div key={e} className="flex items-center gap-2">
                  <RadioGroupItem value={e} id={`email-${e}`} />
                  <Label htmlFor={`email-${e}`} className="font-normal">
                    {e}
                  </Label>
                </div>
              ))}
            </RadioGroup>
          </fieldset>
        ) : email ? (
          <p className="text-sm text-muted-foreground">
            You'll sign in as <span className="font-medium text-foreground">{email}</span>.
          </p>
        ) : null}
        {noEmail && problem?.field !== "form" ? (
          <p role="status" className="text-sm text-muted-foreground">
            None of your sign-in accounts gave an email address, so a password can't be set.
          </p>
        ) : null}
        <PasswordField name="next" label="New password" problem={problem} />
        <PasswordField name="confirm" label="Confirm new password" problem={problem} />
        <FormProblem problem={problem} />
        <Button type="submit" disabled={busy || !email}>
          {busy ? "Saving…" : "Set password"}
        </Button>
      </form>
    </div>
  );
}
