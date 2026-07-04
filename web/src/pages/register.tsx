import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Trans } from "react-i18next";
import { toast } from "sonner";

import { apiBaseUrl } from "@/config";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/components/ui/password-input";
import { LoginIllustration } from "@/components/login-illustration";

// RegisterPage is the only self-service way accounts are created. On a
// fresh instance the first registration claims it (owner of the single org
// in standalone); afterwards availability follows the server's policy
// (REGISTRATION_ENABLED, single-org rule) reported by auth/state. Mirrors
// the login page layout.
export default function RegisterPage() {
  const navigate = useNavigate();
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [bootstrap, setBootstrap] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    fetch(`${apiBaseUrl}/api/v1/auth/state`)
      .then((res) => res.json())
      .then((body) => {
        if (cancelled) return;
        setEnabled(Boolean(body?.data?.registration_enabled));
        setBootstrap(Boolean(body?.data?.bootstrap));
      })
      .catch(() => setEnabled(true)); // fail open in UI; the API enforces
    return () => {
      cancelled = true;
    };
  }, []);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const name = String(form.get("name") ?? "").trim();
    const email = String(form.get("email") ?? "").trim();
    const password = String(form.get("password") ?? "");
    const confirm = String(form.get("confirm") ?? "");
    if (password.length < 4) {
      toast.error("Password must be at least 4 characters");
      return;
    }
    if (password !== confirm) {
      toast.error("Passwords do not match");
      return;
    }
    setBusy(true);
    try {
      const res = await fetch(`${apiBaseUrl}/api/v1/auth/register`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, email, password }),
      });
      const body = await res.json().catch(() => null);
      if (!res.ok || !body?.success) {
        throw new Error(body?.message ?? `Request failed: ${res.status}`);
      }
      toast.success("Account created — log in to continue");
      navigate("/login", { state: { email } });
    } catch (e) {
      toast.error("Registration failed", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="bg-muted flex min-h-svh flex-col items-center justify-center p-6 md:p-10">
      <div className="w-full max-w-sm md:max-w-4xl">
        <div className="flex flex-col gap-6">
          <Card className="overflow-hidden p-0">
            <CardContent className="grid p-0 md:grid-cols-2">
              {enabled === false ? (
                <div className="flex flex-col items-center justify-center gap-4 p-6 text-center md:p-8">
                  <h1 className="text-2xl font-bold">
                    Registration is disabled
                  </h1>
                  <p className="text-muted-foreground text-balance">
                    Ask an administrator to create an account for you.
                  </p>
                  <Button asChild variant="outline">
                    <Link to="/login">Back to login</Link>
                  </Button>
                </div>
              ) : (
                <form
                  className="p-6 md:p-8"
                  onSubmit={(e) => void handleSubmit(e)}
                >
                  <FieldGroup>
                    <div className="flex flex-col items-center gap-2 text-center">
                      <h1 className="text-2xl font-bold">
                        {bootstrap
                          ? "Create the admin account"
                          : "Create an account"}
                      </h1>
                      <p className="text-muted-foreground text-balance">
                        {bootstrap
                          ? "This account administers the instance"
                          : "Sign up for your Winterflow account"}
                      </p>
                    </div>
                    <Field>
                      <FieldLabel htmlFor="reg-name">Name</FieldLabel>
                      <Input id="reg-name" name="name" maxLength={64} required />
                    </Field>
                    <Field>
                      <FieldLabel htmlFor="reg-email">Email</FieldLabel>
                      <Input
                        id="reg-email"
                        name="email"
                        type="email"
                        autoComplete="email"
                        placeholder="you@example.com"
                        required
                      />
                    </Field>
                    <Field>
                      <FieldLabel htmlFor="reg-password">Password</FieldLabel>
                      <PasswordInput
                        id="reg-password"
                        name="password"
                        required
                        toggleLabel="Show password"
                        hideLabel="Hide password"
                      />
                    </Field>
                    <Field>
                      <FieldLabel htmlFor="reg-confirm">
                        Confirm password
                      </FieldLabel>
                      <PasswordInput
                        id="reg-confirm"
                        name="confirm"
                        required
                        toggleLabel="Show password"
                        hideLabel="Hide password"
                      />
                    </Field>
                    <Field>
                      <Button type="submit" className="w-full" disabled={busy}>
                        {busy ? "Creating…" : "Create account"}
                      </Button>
                    </Field>
                    <FieldDescription className="text-center text-sm">
                      Already have an account?{" "}
                      <Link
                        to="/login"
                        className="underline underline-offset-2"
                      >
                        Log in
                      </Link>
                    </FieldDescription>
                  </FieldGroup>
                </form>
              )}
              <LoginIllustration className="hidden h-full md:block" />
            </CardContent>
          </Card>
          <FieldDescription className="px-6 text-center">
            <Trans
              i18nKey="login.terms"
              components={{
                WinterflowLink: (
                  <a
                    href="https://winterflow.io/"
                    className="underline-offset-2 hover:underline"
                    target={"_blank"}
                  />
                ),
                TermsLink: (
                  <a
                    href="https://winterflow.io/legal/terms-of-service/"
                    className="underline-offset-2 hover:underline"
                    target={"_blank"}
                  />
                ),
                PrivacyLink: (
                  <a
                    href="https://winterflow.io/legal/privacy-policy/"
                    className="underline-offset-2 hover:underline"
                    target={"_blank"}
                  />
                ),
              }}
            />
          </FieldDescription>
        </div>
      </div>
    </div>
  );
}
