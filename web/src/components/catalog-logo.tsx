import { useState } from "react";

import { AppIcon } from "@/components/app-icon";
import type { CatalogMeta, TemplateSummary } from "@/lib/templates";
import { cn } from "@/lib/utils";

type LogoTemplate = Pick<TemplateSummary, "id" | "source" | "name"> & { catalog?: Pick<CatalogMeta, "logo_url"> | null };

// The colored initial remains behind the catalog image until it loads and is
// the fallback for a missing/broken logo.
export function CatalogLogo({
  template,
  icon,
  color,
  className,
}: {
  template: LogoTemplate;
  icon?: string;
  color?: string;
  className?: string;
}) {
  const logo = template.source === "catalog" ? template.catalog?.logo_url : "";
  const key = `${template.id}:${logo ?? ""}`;
  const [loaded, setLoaded] = useState("");
  const [failed, setFailed] = useState("");
  return (
    <span className={cn("relative inline-flex shrink-0", className)}>
      <AppIcon name={template.name} icon={icon} color={color} className="size-full" />
      {logo && failed !== key ? (
        <img
          src={logo}
          alt=""
          loading="lazy"
          referrerPolicy="no-referrer"
          onLoad={() => setLoaded(key)}
          onError={() => setFailed(key)}
          className={cn("absolute inset-0 size-full rounded-md bg-card object-contain", loaded === key ? "opacity-100" : "opacity-0")}
        />
      ) : null}
    </span>
  );
}
