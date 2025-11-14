import { Outlet } from "react-router-dom"
import { useTranslation } from "react-i18next"

import { Logo, LogoSprite } from "@/components/app-logo"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/context/auth-context"

export function AppLayout() {
  const { logout } = useAuth()
  const { t } = useTranslation()

  return (
    <div className="flex min-h-screen flex-col bg-background text-foreground">
      <LogoSprite />

      <header className="border-b border-border/50 bg-card/40 px-6 py-3 backdrop-blur">
        <div className="mx-auto flex w-full max-w-6xl items-center justify-between gap-3">
          <Logo size="sm" className="text-primary" />
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              logout()
            }}
          >
            {t("auth.logout")}
          </Button>
        </div>
      </header>

      <main className="flex-1">
        <Outlet />
      </main>
    </div>
  )
}
