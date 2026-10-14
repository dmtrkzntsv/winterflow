import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { apiBase } from "@/config";
import { useAuth } from "@/context/use-auth";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/components/ui/password-input";
import { Spinner } from "@/components/ui/spinner";
import { AuthHeading, AuthScreen } from "@/components/auth-screen";
import { fetchAuthState, type AuthState } from "@/lib/auth-state";

type Errors = { password?: string; confirm?: string; form?: string };

// RegisterPage is /register: on a fresh instance the setup screen ("Set up
// Winterflow" — the first account claims the instance and its
// organization), afterwards the sign-up form when the server's policy
// (REGISTRATION_ENABLED, the single-org rule) allows one. When the state
// can't be read the form is offered: the API enforces the policy.
export default function RegisterPage() {
  const navigate = useNavigate();
  const { login } = useAuth();
  const [state, setState] = useState<AuthState | null>(null);
  const [busy, setBusy] = useState(false);
  const [errors, setErrors] = useState<Errors>({});

  useEffect(() => {
    const ctl = new AbortController();
    fetchAuthState(ctl.signal).then(setState, () => {
      if (!ctl.signal.aborted) setState({ bootstrap: false, registrationEnabled: true });
    });
    return () => ctl.abort();
  }, []);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const name = String(form.get("name") ?? "").trim();
    const email = String(form.get("email") ?? "").trim();
    const password = String(form.get("password") ?? "");
    const confirm = String(form.get("confirm") ?? "");
    const next: Errors = {};
    if (password.length < 4) next.password = "Use at least 4 characters.";
    else if (password !== confirm) next.confirm = "The passwords don't match.";
    setErrors(next);
    if (next.password || next.confirm) return;
    setBusy(true);
    try {
      const res = await fetch(`${apiBase}/api/v1/auth/register`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, email, password }),
      });
      const body = (await res.json().catch(() => null)) as { success?: boolean; message?: string } | null;
      if (!res.ok || !body?.success) {
        throw new Error(body?.message ?? `Request failed: ${res.status}`);
      }
      // Sign the fresh account in right away; if that somehow fails, fall
      // back to the login page with the email prefilled.
      try {
        await login({ username: email, password });
        toast.success("Welcome to Winterflow!");
        navigate("/", { replace: true });
      } catch {
        toast.success("Account created — log in to continue");
        navigate("/login", { state: { email } });
      }
    } catch (e) {
      setErrors({ form: e instanceof Error ? e.message : "Registration failed" });
    } finally {
      setBusy(false);
    }
  };

  if (state === null) {
    return (
      <AuthScreen>
        <div className="flex min-h-64 items-center justify-center">
          <Spinner />
        </div>
      </AuthScreen>
    );
  }

  const bootstrap = state.bootstrap;
  if (!bootstrap && !state.registrationEnabled) {
    return (
      <AuthScreen>
        <div className="flex min-h-64 flex-col items-center justify-center gap-6">
          <AuthHeading title="Registration is closed" subtitle="Ask someone in your organization to create an account for you." />
          <Button asChild variant="outline" className="min-h-10">
            <Link to="/login">Back to log in</Link>
          </Button>
        </div>
      </AuthScreen>
    );
  }

  return (
    <AuthScreen>
      <form onSubmit={(e) => void handleSubmit(e)}>
        <FieldGroup>
          <AuthHeading
            title={bootstrap ? "Set up Winterflow" : "Create an account"}
            subtitle={
              bootstrap
                ? "Create the first account. It manages this Winterflow and can add other people later."
                : "Sign up for your Winterflow account."
            }
          />
          <Field>
            <FieldLabel htmlFor="reg-name">Name</FieldLabel>
            <Input id="reg-name" name="name" autoComplete="name" maxLength={64} required />
          </Field>
          <Field>
            <FieldLabel htmlFor="reg-email">Email</FieldLabel>
            <Input id="reg-email" name="email" type="email" autoComplete="email" placeholder="you@example.com" required />
          </Field>
          <Field data-invalid={errors.password ? true : undefined}>
            <FieldLabel htmlFor="reg-password">Password</FieldLabel>
            <PasswordInput
              id="reg-password"
              name="password"
              autoComplete="new-password"
              required
              aria-invalid={errors.password ? true : undefined}
              aria-describedby={errors.password ? "reg-password-error" : undefined}
              toggleLabel="Show password"
              hideLabel="Hide password"
            />
            {errors.password ? <FieldError id="reg-password-error">{errors.password}</FieldError> : null}
          </Field>
          <Field data-invalid={errors.confirm ? true : undefined}>
            <FieldLabel htmlFor="reg-confirm">Confirm password</FieldLabel>
            <PasswordInput
              id="reg-confirm"
              name="confirm"
              autoComplete="new-password"
              required
              aria-invalid={errors.confirm ? true : undefined}
              aria-describedby={errors.confirm ? "reg-confirm-error" : undefined}
              toggleLabel="Show password"
              hideLabel="Hide password"
            />
            {errors.confirm ? <FieldError id="reg-confirm-error">{errors.confirm}</FieldError> : null}
          </Field>
          {errors.form ? <FieldError>{errors.form}</FieldError> : null}
          <Field>
            <Button type="submit" className="w-full" disabled={busy}>
              {busy ? (bootstrap ? "Setting up…" : "Creating…") : bootstrap ? "Set up Winterflow" : "Create account"}
            </Button>
          </Field>
          {bootstrap ? null : (
            <FieldDescription className="text-center text-sm">
              Already have an account?{" "}
              <Link to="/login" className="underline underline-offset-2">
                Log in
              </Link>
            </FieldDescription>
          )}
        </FieldGroup>
      </form>
    </AuthScreen>
  );
}
