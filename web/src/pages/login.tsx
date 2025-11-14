import { useEffect, useState } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import { useTranslation } from "react-i18next"

import { LoginForm } from "@/components/login-form"
import { useAuth } from "@/context/auth-context"

export default function LoginPage() {
    const { login, isAuthenticated } = useAuth()
    const navigate = useNavigate()
    const location = useLocation()
    const { t } = useTranslation()
    const [isSubmitting, setIsSubmitting] = useState(false)
    const [error, setError] = useState<string | null>(null)

    useEffect(() => {
        if (isAuthenticated) {
            navigate("/", { replace: true })
        }
    }, [isAuthenticated, navigate])

    const handleSubmit = async (values: { email: string; password: string }) => {
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

    return (
        <div className="bg-muted flex min-h-svh flex-col items-center justify-center p-6 md:p-10">
            <div className="w-full max-w-sm md:max-w-4xl">
                <LoginForm
                    onSubmitCredentials={handleSubmit}
                    isSubmitting={isSubmitting}
                    error={error}
                />
            </div>
        </div>
    )
}
