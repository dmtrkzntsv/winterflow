import { Card, CardContent } from "@/components/ui/card";
import { AppIcon } from "@/components/app-icon";
import { useApps } from "@/context/use-apps";

// AppGrid renders the active server's apps as a card grid in the main container
// (v1 dashboard parity).
export function AppGrid() {
  const { apps, loading, error } = useApps();

  if (loading && apps.length === 0) {
    return (
      <div className="grid auto-rows-min gap-4 md:grid-cols-3">
        <div className="bg-muted/50 aspect-video rounded-xl" />
        <div className="bg-muted/50 aspect-video rounded-xl" />
        <div className="bg-muted/50 aspect-video rounded-xl" />
      </div>
    );
  }

  if (error) {
    return <p className="text-sm text-destructive">{error}</p>;
  }

  if (apps.length === 0) {
    return (
      <div className="flex min-h-40 flex-col items-center justify-center rounded-xl border border-dashed text-center">
        <p className="font-medium">No apps yet</p>
        <p className="mt-1 text-sm text-muted-foreground">
          Deploy an application to see it here.
        </p>
      </div>
    );
  }

  return (
    <div className="grid auto-rows-min gap-4 md:grid-cols-3">
      {apps.map((app) => (
        <Card
          key={app.id}
          className="cursor-pointer transition-colors hover:bg-muted/50"
        >
          <CardContent className="flex items-center gap-3 p-4">
            <AppIcon name={app.name} color={app.color} className="size-10" />
            <div className="min-w-0">
              <div className="truncate font-medium">{app.name}</div>
              <div className="truncate text-xs text-muted-foreground">
                {app.version || "—"}
              </div>
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  );
}
