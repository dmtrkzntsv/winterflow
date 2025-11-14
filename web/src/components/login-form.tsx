import type { ComponentPropsWithoutRef, FormEvent } from "react"
import { Trans, useTranslation } from "react-i18next"

import { cn } from "@/lib/utils"
import { apiBaseUrl, appBaseUrl } from "@/config"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import {
    Field,
    FieldDescription,
    FieldGroup,
    FieldLabel,
    FieldSeparator,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { PasswordInput } from "@/components/ui/password-input"
import { LoginIllustration } from "@/components/login-illustration"

type LoginFormValues = {
  username: string
  password: string
}

type LoginFormProps = ComponentPropsWithoutRef<"div"> & {
  onSubmitCredentials?: (values: LoginFormValues) => void | Promise<void>
  isSubmitting?: boolean
  error?: string | null
  availableProviders?: string[]
  providersLoading?: boolean
}

export function LoginForm({
    className,
    onSubmitCredentials,
    isSubmitting = false,
    error,
    availableProviders = ["local"],
    providersLoading = false,
    ...props
}: LoginFormProps) {
    const { t } = useTranslation()
    const providerSet = new Set(availableProviders)
    const localEnabled = providerSet.has("local") && !providersLoading

    const socialProviders = [
        {
            id: "google",
            icon: (
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">
                    <path
                        d="M12.48 10.92v3.28h7.84c-.24 1.84-.853 3.187-1.787 4.133-1.147 1.147-2.933 2.4-6.053 2.4-4.827 0-8.6-3.893-8.6-8.72s3.773-8.72 8.6-8.72c2.6 0 4.507 1.027 5.907 2.347l2.307-2.307C18.747 1.44 16.133 0 12.48 0 5.867 0 .307 5.387.307 12s5.56 12 12.173 12c3.573 0 6.267-1.173 8.373-3.36 2.16-2.16 2.84-5.213 2.84-7.667 0-.76-.053-1.467-.173-2.053H12.48z"
                        fill="currentColor"
                    />
                </svg>
            ),
            labelKey: "login.providers.google",
        },
    ] as const

    const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault()
        if (!onSubmitCredentials) {
            return
        }
        const formData = new FormData(event.currentTarget)
        onSubmitCredentials({
            username: String(formData.get("username") ?? ""),
            password: String(formData.get("password") ?? ""),
        })
    }

    return (
        <div className={cn("flex flex-col gap-6", className)} {...props}>
            <Card className="overflow-hidden p-0">
                <CardContent className="grid p-0 md:grid-cols-2">
                    <form className="p-6 md:p-8" onSubmit={handleSubmit}>
                        <FieldGroup
                            className={cn({ "opacity-60": !localEnabled })}
                            aria-disabled={!localEnabled}
                        >
                            <div className="flex flex-col items-center gap-2 text-center">
                                <h1 className="text-2xl font-bold">{t("login.title")}</h1>
                                <p className="text-muted-foreground text-balance">
                                    {t("login.subtitle")}
                                </p>
                            </div>
                            <Field>
                                <FieldLabel htmlFor="username">{t("login.usernameLabel")}</FieldLabel>
                                <Input
                                    id="username"
                                    name="username"
                                    type="text"
                                    placeholder={t("login.usernamePlaceholder")}
                                    disabled={!localEnabled}
                                    required
                                />
                            </Field>
                            <Field>
                                <FieldLabel htmlFor="password">{t("login.passwordLabel")}</FieldLabel>
                                <PasswordInput
                                    id="password"
                                    name="password"
                                    disabled={!localEnabled}
                                    required
                                    toggleLabel={t("login.showPassword")}
                                    hideLabel={t("login.hidePassword")}
                                />
                            </Field>
                            <Field>
                                <Button
                                    type="submit"
                                    disabled={isSubmitting || !localEnabled}
                                    className="w-full"
                                >
                                    {isSubmitting ? t("login.submitting") : t("login.submit")}
                                </Button>
                            </Field>
                            {error ? (
                                <FieldDescription
                                    className="text-destructive text-sm"
                                    role="status"
                                    aria-live="assertive"
                                >
                                    {error}
                                </FieldDescription>
                            ) : null}
                            <FieldSeparator className="*:data-[slot=field-separator-content]:bg-card">
                                {t("login.ssoLabel")}
                            </FieldSeparator>
                            <div className="flex justify-center gap-4">
                                {socialProviders.map((provider) => {
                                    const isAvailable = providerSet.has(provider.id) && !providersLoading
                                    return (
                                        <span key={provider.id} className="inline-flex">
                                            <Button
                                                variant="outline"
                                                type="button"
                                                disabled={!isAvailable}
                                                className="min-w-16"
                                                onClick={() => {
                                                    if (!isAvailable) return
                                                    const siteUrl = window.location.origin
                                                    let siteHost = siteUrl
                                                    try {
                                                        siteHost = new URL(siteUrl).hostname
                                                    } catch {
                                                        try {
                                                            siteHost = new URL(appBaseUrl).hostname
                                                        } catch {
                                                            siteHost = siteUrl
                                                        }
                                                    }
                                                    window.location.href = `${apiBaseUrl}/auth/${provider.id}/login?site=${encodeURIComponent(
                                                        siteHost,
                                                    )}&from=${encodeURIComponent(appBaseUrl)}`
                                                }}
                                            >
                                                {provider.icon}
                                                <span className="sr-only">{t(provider.labelKey)}</span>
                                            </Button>
                                        </span>
                                    )
                                })}
                            </div>
                        </FieldGroup>
                    </form>
                    <LoginIllustration className="hidden h-full md:block" />
                </CardContent>
            </Card>
            <FieldDescription className="px-6 text-center">
                <Trans
                    i18nKey="login.terms"
                    components={{
                        TermsLink: (
                            <a href="https://winterflow.io/legal/terms-of-service/" className="underline-offset-2 hover:underline" target={"_blank"} />
                        ),
                        PrivacyLink: (
                            <a href="https://winterflow.io/legal/privacy-policy/" className="underline-offset-2 hover:underline" target={"_blank"} />
                        ),
                    }}
                />
            </FieldDescription>
        </div>
    )
}
