import { useEffect, useMemo, useState } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"

import { LoginForm } from "@/components/login-form"
import { useAuth } from "@/context/auth-context"
import { apiBaseUrl } from "@/config"

export default function LoginPage() {
    const { login, isAuthenticated } = useAuth()
    const navigate = useNavigate()
    const location = useLocation()
    const { t } = useTranslation()
    const [isSubmitting, setIsSubmitting] = useState(false)
    const [error, setError] = useState<string | null>(null)
    const [providers, setProviders] = useState<string[]>([])
    const [providersLoading, setProvidersLoading] = useState(true)

    useEffect(() => {
        let cancelled = false
        const fetchProviders = async () => {
            try {
                const response = await fetch(`${apiBaseUrl}/auth/list`)
                if (!response.ok) {
                    throw new Error(`Failed to load providers: ${response.status}`)
                }
                const data = (await response.json()) as unknown
                if (!Array.isArray(data)) {
                    throw new Error("Invalid provider response")
                }
                const parsed = data.filter(
                    (provider): provider is string => typeof provider === "string",
                )
                if (!cancelled) {
                    setProviders(parsed)
                }
            } catch (fetchError) {
                console.error("Failed to fetch providers", fetchError)
                if (!cancelled) {
                    setProviders(["local"])
                }
            } finally {
                if (!cancelled) {
                    setProvidersLoading(false)
                }
            }
        }

        void fetchProviders()
        return () => {
            cancelled = true
        }
    }, [])

    useEffect(() => {
        if (isAuthenticated) {
            navigate("/", { replace: true })
        }
    }, [isAuthenticated, navigate])

    const handleSubmit = async (values: { username: string; password: string }) => {
        setIsSubmitting(true)
        setError(null)
        try {
            await login(values)
            const redirectTo =
                (location.state as { from?: { pathname?: string } } | undefined)?.from?.pathname ??
                "/"
            navigate(redirectTo, { replace: true })
        } catch (err) {
            console.error("login failed", err)
            setError(t("login.errorGeneric"))
        } finally {
            setIsSubmitting(false)
        }
    }

    const effectiveProviders = useMemo(() => {
        if (providers.length === 0) {
            return ["local"]
        }
        return providers
    }, [providers])

    return (
        <div className="bg-muted flex min-h-svh flex-col items-center justify-center p-6 md:p-10">
            <div className="w-full max-w-sm md:max-w-4xl">
                <LoginForm
                    onSubmitCredentials={handleSubmit}
                    isSubmitting={isSubmitting}
                    error={error}
                    availableProviders={effectiveProviders}
                    providersLoading={providersLoading}
                />
            </div>
        </div>
    )
}
