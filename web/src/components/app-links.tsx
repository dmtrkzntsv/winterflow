import { Link } from "react-router-dom";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

// AppLinks names the apps (ids) using something, each linking to its app:
// as chips, or inline after their count ("2 apps: Shop, Blog"). None reads
// "Not used".
export function AppLinks({ ids, names, chips = false }: { ids: string[]; names: Map<string, string>; chips?: boolean }) {
  if (ids.length === 0) return <span className="text-muted-foreground">Not used</span>;
  const link = (id: string, className?: string) => (
    <Link to={`/app/${id}`} className={cn(className, "underline-offset-4 hover:underline")}>
      {names.get(id) ?? id}
    </Link>
  );
  if (chips) {
    return (
      <span className="flex flex-wrap gap-1">
        {ids.map((id) => (
          <Badge key={id} variant="secondary" className="whitespace-nowrap font-medium">
            {link(id)}
          </Badge>
        ))}
      </span>
    );
  }
  return (
    <span>
      {ids.length === 1 ? "1 app: " : `${ids.length} apps: `}
      {ids.map((id, i) => (
        <span key={id}>
          {i > 0 && ", "}
          {link(id, "font-medium")}
        </span>
      ))}
    </span>
  );
}
