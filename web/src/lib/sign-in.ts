// Provider sign-in helpers: the start URL and the callback's error codes.

// signInErrorText is what a provider sign-in or link that failed says: the
// API's callback sends the browser back with ?error=<code>.
const messages: Record<string, string> = {
  invalid_state: "The sign-in expired or was started in another tab. Try again.",
  denied: "The sign-in was cancelled.",
  provider_error: "The sign-in provider couldn't sign you in. Try again.",
  email_required: "The provider didn't share your email address, which Winterflow needs.",
  email_unverified: "The provider hasn't verified your email address.",
  account_exists:
    "An account with this email already exists. Sign in with your password, then link the provider in Settings → Sign-in methods.",
  registration_closed: "There's no Winterflow account for this email, and registration is closed.",
  session_mismatch: "You're signed in as someone else now. Sign in again and retry.",
  identity_taken: "That provider account is already linked to another Winterflow account.",
  provider_already_linked: "You already linked an account from that provider. Unlink it first.",
};

export function signInErrorText(code: string): string {
  return messages[code] ?? "The sign-in didn't work. Try again.";
}

// providerStartURL is where a provider button goes: the API's start route
// for mode (login or link) that comes back to returnTo.
export function providerStartURL(base: string, id: string, mode: "login" | "link", returnTo: string): string {
  const q = new URLSearchParams({ mode, return_to: returnTo });
  return `${base}/auth/oauth/${encodeURIComponent(id)}/start?${q}`;
}
