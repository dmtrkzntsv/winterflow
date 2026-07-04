import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { apiBaseUrl } from "@/config";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PasswordInput } from "@/components/ui/password-input";

// RegisterPage is the only self-service way accounts are created. On a
// fresh instance the first registration claims it (owner of the single org
// in standalone); afterwards availability follows the server's policy
// (REGISTRATION_ENABLED, single-org rule) reported by auth/state.
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
    if (password.length < 8) {
      toast.error("Password must be at least 8 characters");
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
      <div className="w-full max-w-sm">
        <Card>
          <CardHeader>
            <CardTitle>
              {bootstrap ? "Create the admin account" : "Create an account"}
            </CardTitle>
          </CardHeader>
          <CardContent>
            {enabled === false ? (
              <div className="space-y-4">
                <p className="text-sm text-muted-foreground">
                  Registration is disabled on this instance. Ask an
                  administrator to create an account for you.
                </p>
                <Button asChild variant="outline" className="w-full">
                  <Link to="/login">Back to login</Link>
                </Button>
              </div>
            ) : (
              <form
                className="space-y-4"
                onSubmit={(e) => void handleSubmit(e)}
              >
                {bootstrap ? (
                  <p className="text-sm text-muted-foreground">
                    This instance has no users yet — this account becomes the
                    administrator.
                  </p>
                ) : null}
                <div className="space-y-2">
                  <Label htmlFor="reg-name">Name</Label>
                  <Input id="reg-name" name="name" maxLength={64} required />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="reg-email">Email</Label>
                  <Input
                    id="reg-email"
                    name="email"
                    type="email"
                    autoComplete="email"
                    required
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="reg-password">Password</Label>
                  <PasswordInput
                    id="reg-password"
                    name="password"
                    required
                    toggleLabel="Show password"
                    hideLabel="Hide password"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="reg-confirm">Confirm password</Label>
                  <PasswordInput
                    id="reg-confirm"
                    name="confirm"
                    required
                    toggleLabel="Show password"
                    hideLabel="Hide password"
                  />
                </div>
                <Button type="submit" className="w-full" disabled={busy}>
                  {busy ? "Creating…" : "Create account"}
                </Button>
                <p className="text-center text-sm text-muted-foreground">
                  Already have an account?{" "}
                  <Link to="/login" className="underline underline-offset-2">
                    Log in
                  </Link>
                </p>
              </form>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
