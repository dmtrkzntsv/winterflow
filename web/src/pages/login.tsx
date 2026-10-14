import { useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { AuthScreen } from "@/components/auth-screen";
import { LoginForm } from "@/components/login-form";
import { apiBaseUrl } from "@/config";
import { useAuth } from "@/context/use-auth";
import { fetchAuthState } from "@/lib/auth-state";

type LoginState = { from?: { pathname?: string; search?: string; hash?: string }; email?: string } | undefined;

// LoginPage is /login. A fresh (unclaimed) instance has no account to log
// in to, so it goes to the setup screen (/register) instead; when the
// instance's state can't be read the form stays (and offers no sign-up).
export default function LoginPage() {
  const { login, isAuthenticated } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const { t } = useTranslation();
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [providers, setProviders] = useState<string[]>([]);
  const [providersLoading, setProvidersLoading] = useState(true);
  const [registerEnabled, setRegisterEnabled] = useState(false);
  const state = location.state as LoginState;
  // Back to where the person was headed, query and hash included (an
  // alert e-mail's ?tab=metrics link keeps its tab).
  const from = state?.from;
  const redirectTo = from?.pathname ? `${from.pathname}${from.search ?? ""}${from.hash ?? ""}` : "/";

  useEffect(() => {
    const ctl = new AbortController();
    fetchAuthState(ctl.signal).then(
      (s) => {
        if (s.bootstrap) {
          navigate("/register", { replace: true });
          return;
        }
        setRegisterEnabled(s.registrationEnabled);
      },
      () => {
        // Unknown state: keep the form, without a sign-up link.
      },
    );
    return () => ctl.abort();
  }, [navigate]);

  useEffect(() => {
    let cancelled = false;
    const fetchProviders = async () => {
      try {
        const response = await fetch(`${apiBaseUrl}/auth/list`);
        if (!response.ok) {
          throw new Error(`Failed to load providers: ${response.status}`);
        }
        const data = (await response.json()) as unknown;
        if (!Array.isArray(data)) {
          throw new Error("Invalid provider response");
        }
        const parsed = data.filter((provider): provider is string => typeof provider === "string");
        if (!cancelled) setProviders(parsed);
      } catch (fetchError) {
        console.error("Failed to fetch providers", fetchError);
        if (!cancelled) setProviders(["local"]);
      } finally {
        if (!cancelled) setProvidersLoading(false);
      }
    };
    void fetchProviders();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (isAuthenticated) navigate(redirectTo, { replace: true });
  }, [isAuthenticated, navigate, redirectTo]);

  const handleSubmit = async (values: { username: string; password: string }) => {
    setIsSubmitting(true);
    setError(null);
    try {
      await login(values);
      navigate(redirectTo, { replace: true });
    } catch (err) {
      console.error("login failed", err);
      setError(t("login.errorGeneric"));
    } finally {
      setIsSubmitting(false);
    }
  };

  const effectiveProviders = useMemo(() => (providers.length === 0 ? ["local"] : providers), [providers]);

  return (
    <AuthScreen>
      <LoginForm
        onSubmitCredentials={handleSubmit}
        isSubmitting={isSubmitting}
        error={error}
        availableProviders={effectiveProviders}
        providersLoading={providersLoading}
        registerEnabled={registerEnabled}
        defaultEmail={state?.email ?? ""}
      />
    </AuthScreen>
  );
}
