import { useEffect } from "react"
import { useOutletContext } from "react-router-dom"

import type {
  AppLayoutOutletContext,
  BreadcrumbEntry,
} from "@/layouts/app-layout"

export function useAppBreadcrumbs(breadcrumbs: BreadcrumbEntry[]) {
  const { setBreadcrumbs } = useOutletContext<AppLayoutOutletContext>()

  useEffect(() => {
    setBreadcrumbs(breadcrumbs)
  }, [breadcrumbs, setBreadcrumbs])
}
