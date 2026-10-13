import type { ReactNode } from "react";

import { ErrorIcon } from "@/components/inline-error";
import { cn } from "@/lib/utils";

// lastLine is an error's last non-empty line: with docker and compose
// output, the line that says what went wrong.
function lastLine(text: string): string {
  const lines = text.split("\n").map((l) => l.trim()).filter(Boolean);
  return lines.at(-1) ?? "";
}

// ErrorText shows an agent or docker error by its last line, the full text
// behind Details (only when there is more to it). prefix and suffix frame
// the line; alert announces it to screen readers; icon leads with a cross.
export function ErrorText({
  text,
  prefix,
  suffix,
  alert,
  icon,
  className,
}: {
  text: string;
  prefix?: ReactNode;
  suffix?: ReactNode;
  alert?: boolean;
  icon?: boolean;
  className?: string;
}) {
  const line = lastLine(text);
  const more = line !== text.trim();
  return (
    <div role={alert ? "alert" : undefined} className={cn("text-sm text-destructive", className)}>
      <p className={cn("break-words", icon && "flex items-start gap-1.5")}>
        {icon ? <ErrorIcon /> : null}
        <span>
          {prefix}
          {line}
          {suffix}
        </span>
      </p>
      {more ? (
        <details className="mt-1">
          <summary className="w-fit cursor-pointer text-xs text-muted-foreground hover:text-foreground">Details</summary>
          <pre className="mt-1 max-h-60 overflow-auto rounded-md border bg-muted/40 p-2 font-mono text-xs break-words whitespace-pre-wrap text-foreground">
            {text}
          </pre>
        </details>
      ) : null}
    </div>
  );
}
