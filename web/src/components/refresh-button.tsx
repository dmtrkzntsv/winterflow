import { RefreshCw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// RefreshButton lists something again ("Refresh domains"); it is off and
// spinning while a load runs.
export function RefreshButton({ label, loading, onClick }: { label: string; loading: boolean; onClick: () => void }) {
  return (
    <Button
      size="icon"
      variant="ghost"
      className="size-10 text-muted-foreground sm:size-8"
      aria-label={label}
      title={label}
      disabled={loading}
      onClick={onClick}
    >
      <RefreshCw className={cn("size-4", loading && "animate-spin motion-reduce:animate-none")} />
    </Button>
  );
}
