import { Bell, BellRing, Hash, Link, Mail, MessageCircle, Send, Smartphone, Webhook, type LucideIcon } from "lucide-react";

import type { ServiceId } from "@/lib/alerts";

const ICONS: Record<ServiceId, LucideIcon> = {
  telegram: Send,
  discord: MessageCircle,
  slack: Hash,
  ntfy: Bell,
  gotify: BellRing,
  pushover: Smartphone,
  email: Mail,
  webhook: Webhook,
  raw: Link,
};

// ServiceIcon is a notification service's glyph (decoration: the service's
// name is always written next to it).
export function ServiceIcon({ service, className }: { service: ServiceId; className?: string }) {
  const Icon = ICONS[service];
  return <Icon className={className} aria-hidden />;
}
