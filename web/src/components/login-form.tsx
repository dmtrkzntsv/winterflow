import type { FormEvent } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";
import { apiBase } from "@/config";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldSeparator } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/components/ui/password-input";
import { AuthHeading } from "@/components/auth-screen";
import { ProviderIcon } from "@/components/provider-icon";
import type { SignInProvider } from "@/lib/auth-state";
import { providerStartURL } from "@/lib/sign-in";

type LoginFormValues = {
  email: string;
  password: string;
};

type LoginFormProps = {
  onSubmitCredentials?: (values: LoginFormValues) => void | Promise<void>;
  isSubmitting?: boolean;
  error?: string | null;
  // providers: the external sign-ins, one button each.
  providers?: SignInProvider[];
  // returnTo: where a provider sign-in comes back to (a same-origin path).
  returnTo?: string;
  // registerEnabled: show the "create an account" link.
  registerEnabled?: boolean;
  defaultEmail?: string;
  className?: string;
};

// LoginForm is the log-in form (email + password, the sign-up link, a
// button per sign-in provider); the page puts it in an AuthScreen.
export function LoginForm({
  onSubmitCredentials,
  isSubmitting = false,
  error,
  providers = [],
  returnTo = "/",
  registerEnabled = false,
  defaultEmail = "",
  className,
}: LoginFormProps) {
  const { t } = useTranslation();

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!onSubmitCredentials) return;
    const formData = new FormData(event.currentTarget);
    void onSubmitCredentials({
      email: String(formData.get("username") ?? ""),
      password: String(formData.get("password") ?? ""),
    });
  };
  return (
    <form className={cn(className)} onSubmit={handleSubmit}>
      <FieldGroup>
        <AuthHeading title={t("login.title")} subtitle={t("login.subtitle")} />
        <Field>
          <FieldLabel htmlFor="username">{t("login.usernameLabel")}</FieldLabel>
          <Input
            id="username"
            name="username"
            type="email"
            autoComplete="email"
            placeholder={t("login.usernamePlaceholder")}
            defaultValue={defaultEmail}
            required
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="password">{t("login.passwordLabel")}</FieldLabel>
          <PasswordInput
            id="password"
            name="password"
            autoComplete="current-password"
            required
            toggleLabel={t("login.showPassword")}
            hideLabel={t("login.hidePassword")}
          />
        </Field>
        <Field>
          <Button type="submit" disabled={isSubmitting} className="w-full">
            {isSubmitting ? t("login.submitting") : t("login.submit")}
          </Button>
        </Field>
        {registerEnabled ? (
          <FieldDescription className="text-center text-sm">
            {t("login.noAccount")}{" "}
            <Link to="/register" className="underline underline-offset-2">
              {t("login.registerLink")}
            </Link>
          </FieldDescription>
        ) : null}
        {error ? (
          <FieldDescription className="text-destructive text-sm" role="status" aria-live="assertive">
            {error}
          </FieldDescription>
        ) : null}
        {/* No provider configured: no block (a disabled button is noise). */}
        {providers.length > 0 ? (
          <>
            <FieldSeparator className="*:data-[slot=field-separator-content]:bg-card">{t("login.ssoLabel")}</FieldSeparator>
            <div className="flex flex-col gap-2">
              {providers.map((p) => (
                <Button key={p.id} asChild variant="outline" className="min-h-10 w-full">
                  <a href={providerStartURL(apiBase, p.id, "login", returnTo)}>
                    <ProviderIcon id={p.id} />
                    {t("login.continueWith", { name: p.name })}
                  </a>
                </Button>
              ))}
            </div>
          </>
        ) : null}
      </FieldGroup>
    </form>
  );
}
