import { Badge } from "@/components/ui/badge";
import { TLS_MODES, type DomainTLS } from "@/lib/domains";

// TLSBadge names a registered domain's mode, the same way on the Domains tab
// and under an app's hostnames. "TLS off" is outlined: nothing is wrong with
// it, it just isn't encrypted by this server.
export function TLSBadge({ tls }: { tls: DomainTLS }) {
  return (
    <Badge variant={tls === "off" ? "outline" : "secondary"} className="whitespace-nowrap">
      {TLS_MODES[tls].label}
    </Badge>
  );
}
