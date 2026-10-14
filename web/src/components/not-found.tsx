import { SearchX } from "lucide-react"
import { Link } from "react-router-dom"

import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { usePageTitle } from "@/layouts/use-page-title"

// NotFound is the error page for URLs that address nothing: an unknown route,
// a server outside the org, an app not on the server.
export function NotFound({
  title = "Page not found",
  description = "There is nothing at this address.",
}: {
  title?: string
  description?: string
}) {
  usePageTitle("Not found")
  return (
    <Empty>
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <SearchX />
        </EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{description}</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button asChild variant="outline" size="sm">
          {/* "/" opens the default server's Apps. */}
          <Link to="/">Go to Apps</Link>
        </Button>
      </EmptyContent>
    </Empty>
  )
}
