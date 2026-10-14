import type { ReactNode } from "react";
import { Trans } from "react-i18next";

import { isDistributed } from "@/config";
import { LogoIcon } from "@/components/app-logo";
import { Card, CardContent } from "@/components/ui/card";
import { FieldDescription } from "@/components/ui/field";
import { LoginIllustration } from "@/components/login-illustration";

const linkClass = "underline-offset-2 hover:underline";

// AuthScreen is the frame of every sign-in page (sign in, set up, sign up):
// the form beside the snowfall on wider screens, the form alone on phones
// (the illustration is decoration), and on the hosted service (terms, set
// to distributed) the terms line under the card; a self-hosted install
// has no terms of ours.
export function AuthScreen({ children, terms = isDistributed }: { children: ReactNode; terms?: boolean }) {
  return (
    <div className="bg-muted flex min-h-svh flex-col items-center justify-center p-4 sm:p-6 md:p-10">
      <div className="flex w-full max-w-sm flex-col gap-6 md:max-w-4xl">
        <Card className="overflow-hidden p-0">
          <CardContent className="grid p-0 md:grid-cols-2">
            <div className="p-6 md:p-8">{children}</div>
            <LoginIllustration data-testid="auth-illustration" className="hidden h-full md:block" />
          </CardContent>
        </Card>
        {terms ? (
          <FieldDescription className="px-6 text-center">
            <Trans
              i18nKey="login.terms"
              components={{
                WinterflowLink: <a href="https://winterflow.io/" className={linkClass} target="_blank" rel="noopener noreferrer" />,
                TermsLink: (
                  <a
                    href="https://winterflow.io/legal/terms-of-service/"
                    className={linkClass}
                    target="_blank"
                    rel="noopener noreferrer"
                  />
                ),
                PrivacyLink: (
                  <a
                    href="https://winterflow.io/legal/privacy-policy/"
                    className={linkClass}
                    target="_blank"
                    rel="noopener noreferrer"
                  />
                ),
              }}
            />
          </FieldDescription>
        ) : null}
      </div>
    </div>
  );
}

// AuthHeading is a sign-in page's title (24/600) under the snowflake, and
// one line under it.
export function AuthHeading({ title, subtitle }: { title: string; subtitle: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-2 text-center">
      <LogoIcon size="lg" data-testid="auth-logo" />
      <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
      <p className="text-muted-foreground text-balance">{subtitle}</p>
    </div>
  );
}
