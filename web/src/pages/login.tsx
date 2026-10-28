import { useEffect, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { AuthScreen } from "@/components/auth-screen";
import { LoginForm } from "@/components/login-form";
import { LoginError } from "@/context/auth-context-base";
import { useAuth } from "@/context/use-auth";
import { fetchAuthState, type SignInProvider } from "@/lib/auth-state";
import { returnTarget, type ReturnFrom } from "@/lib/return-path";
import { signInErrorText } from "@/lib/sign-in";

type LoginState = { from?: ReturnFrom; email?: string } | undefined;

// LoginPage is /login. A fresh (unclaimed) instance has no account to log
// in to, so it goes to the setup screen (/register) instead; when the
// instance's state can't be read the form stays (and offers no sign-up and
// no providers). A sign-in, with the password or a provider, goes on to
// ?next= (a same-origin path; the guard sets it), else home. A provider
// sign-in that failed comes back with ?error=<code>.
export default function LoginPage() {
  const { login, isAuthenticated } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [search] = useSearchParams();
  const { t } = useTranslation();
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(() => {
    const code = search.get("error");
    return code ? signInErrorText(code) : null;
  });
  const [providers, setProviders] = useState<SignInProvider[]>([]);
  const [registerEnabled, setRegisterEnabled] = useState(false);
  const state = location.state as LoginState;
  const redirectTo = returnTarget(search, state?.from);

  useEffect(() => {
    const ctl = new AbortController();
    fetchAuthState(ctl.signal).then(
      (s) => {
        if (s.bootstrap) {
          navigate("/register", { replace: true });
          return;
        }
        setRegisterEnabled(s.registrationEnabled);
        setProviders(s.providers);
      },
      () => {
        // Unknown state: keep the form, without a sign-up link.
      },
    );
    return () => ctl.abort();
  }, [navigate]);

  useEffect(() => {
    if (isAuthenticated) navigate(redirectTo, { replace: true });
  }, [isAuthenticated, navigate, redirectTo]);

  const handleSubmit = async (values: { email: string; password: string }) => {
    setIsSubmitting(true);
    setError(null);
    try {
      await login(values);
      navigate(redirectTo, { replace: true });
    } catch (err) {
      console.error("login failed", err);
      const refused = err instanceof LoginError && [400, 401].includes(err.status);
      setError(t(refused ? "login.errorCredentials" : "login.errorGeneric"));
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <AuthScreen>
      <LoginForm
        onSubmitCredentials={handleSubmit}
        isSubmitting={isSubmitting}
        error={error}
        providers={providers}
        returnTo={redirectTo}
        registerEnabled={registerEnabled}
        defaultEmail={state?.email ?? ""}
      />
    </AuthScreen>
  );
}
