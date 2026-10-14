import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ArrowDown, RefreshCw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

export type LogLine = {
  timestamp: number;
  level: number;
  message: string;
  container?: string;
};

const LEVEL_CLASS: Record<number, string> = {
  4: "text-amber-800 dark:text-amber-300",
  5: "text-red-700 dark:text-red-400",
  6: "text-red-700 dark:text-red-400 font-semibold",
};

// Container name colors, in the spirit of `docker compose logs`: each service
// keeps one hue so interleaved output stays easy to follow.
const CONTAINER_COLORS = [
  "text-sky-700 dark:text-sky-400",
  "text-emerald-700 dark:text-emerald-400",
  "text-violet-700 dark:text-violet-400",
  "text-amber-800 dark:text-amber-400",
  "text-pink-700 dark:text-pink-400",
  "text-teal-700 dark:text-teal-300",
];

const TAIL_OPTIONS = [200, 500, 1000] as const;

const timeFormat = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
});

type Props = {
  lines: LogLine[];
  loading: boolean;
  error: string | null;
  tail: number;
  onTailChange: (tail: number) => void;
  onRefresh: () => void;
};

// Chip is one container filter: a toggle button in the console's palette.
function Chip({ pressed, onClick, children }: { pressed: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      onClick={onClick}
      className={cn(
        "inline-flex min-h-7 items-center gap-1.5 rounded-full border border-console-border px-2.5 font-mono text-xs transition-colors duration-150 motion-reduce:transition-none max-sm:min-h-10",
        pressed ? "bg-console-hover text-console-foreground" : "text-console-muted hover:text-console-foreground",
      )}
    >
      {children}
    </button>
  );
}

// LogsView is a console-style log display: a scroll box that sticks to the
// bottom while new lines arrive, unless the user has scrolled up to read
// (then it stays put until they jump back down). It fills what is left of
// its parent (a flex column that grows with the viewport, min-h-80 at
// least), so the app page itself does not scroll.
export function LogsView({
  lines,
  loading,
  error,
  tail,
  onTailChange,
  onRefresh,
}: Props) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [stickToBottom, setStickToBottom] = useState(true);

  const containers = useMemo(() => {
    const seen: string[] = [];
    for (const l of lines) {
      if (l.container && !seen.includes(l.container)) seen.push(l.container);
    }
    return seen;
  }, [lines]);
  const colorOf = (c: string) =>
    CONTAINER_COLORS[containers.indexOf(c) % CONTAINER_COLORS.length];
  const labelWidth = Math.min(
    24,
    containers.reduce((w, c) => Math.max(w, c.length), 0),
  );

  // The container picked in the chips; it is dropped (back to All) when that
  // container has no lines any more, so it can't silently return later.
  const [only, setOnly] = useState<string | null>(null);
  const active = only !== null && containers.includes(only) ? only : null;
  if (only !== null && active === null) setOnly(null);
  const shown = useMemo(() => (active ? lines.filter((l) => l.container === active) : lines), [lines, active]);

  useEffect(() => {
    const el = scrollRef.current;
    if (el && stickToBottom) {
      el.scrollTop = el.scrollHeight;
    }
  }, [shown, stickToBottom]);

  const handleScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    const fromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    setStickToBottom(fromBottom < 40);
  };

  const jumpToBottom = () => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
    setStickToBottom(true);
  };

  return (
    <div className="relative flex min-h-80 grow basis-0 flex-col overflow-hidden rounded-lg border border-console-border bg-console text-console-foreground">
      <div className="flex flex-wrap items-center gap-2 border-b border-console-border px-3 py-2">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5 text-xs">
          {containers.length > 1 ? (
            <div role="group" aria-label="Containers" className="flex flex-wrap gap-1.5">
              <Chip pressed={active === null} onClick={() => setOnly(null)}>
                All
              </Chip>
              {containers.map((c) => (
                <Chip key={c} pressed={active === c} onClick={() => setOnly(c)}>
                  <span className={cn("size-2 rounded-full bg-current", colorOf(c))} aria-hidden />
                  <span>{c}</span>
                </Chip>
              ))}
            </div>
          ) : (
            <span className="font-mono text-console-muted">{containers[0] ?? "Container output"}</span>
          )}
        </div>
        {(loading && lines.length === 0) || error ? null : (
          <span className="text-xs tabular-nums text-console-muted">
            {shown.length === 1 ? "1 line" : `${shown.length} lines`}
          </span>
        )}
        <Select
          value={String(tail)}
          onValueChange={(v) => onTailChange(Number(v))}
        >
          <SelectTrigger
            aria-label="Number of lines"
            className="h-7 w-36 max-sm:h-10 border-console-border bg-console-hover text-xs text-console-foreground"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {TAIL_OPTIONS.map((n) => (
              <SelectItem key={n} value={String(n)}>
                Last {n} lines
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          size="icon"
          variant="ghost"
          aria-label="Refresh logs"
          title="Refresh logs"
          className="size-7 max-sm:size-10 text-console-muted hover:bg-console-hover hover:text-console-foreground"
          onClick={onRefresh}
          disabled={loading}
        >
          <RefreshCw className={cn("size-3.5", loading && "animate-spin motion-reduce:animate-none")} />
        </Button>
      </div>

      {loading && lines.length === 0 ? (
        <div className="flex flex-1 items-center justify-center">
          <Spinner />
        </div>
      ) : error ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
          <p className="text-sm text-red-700 dark:text-red-400">{error}</p>
          <Button
            size="sm"
            variant="outline"
            className="border-console-border bg-transparent text-console-foreground hover:bg-console-hover hover:text-console-foreground"
            onClick={onRefresh}
          >
            Try again
          </Button>
        </div>
      ) : lines.length === 0 ? (
        <div className="flex flex-1 items-center justify-center p-6 text-sm text-console-muted">
          No output yet. Start the app, then refresh.
        </div>
      ) : (
        <div
          ref={scrollRef}
          // Not role="log": that is a live region, and a refresh replaces up
          // to a thousand lines.
          role="region"
          aria-label="Logs"
          tabIndex={0}
          onScroll={handleScroll}
          className="flex-1 overflow-y-auto py-2 font-mono focus-visible:ring-2 focus-visible:ring-console-foreground/60 focus-visible:outline-none focus-visible:ring-inset text-xs leading-5"
        >
          {shown.map((l, i) => (
            <div
              key={i}
              className="flex gap-3 px-3 hover:bg-console-hover"
            >
              <span className="shrink-0 text-console-muted tabular-nums select-none">
                {timeFormat.format(new Date(l.timestamp * 1000))}
              </span>
              {l.container ? (
                <span
                  className={cn("shrink-0 truncate select-none", colorOf(l.container))}
                  style={{ width: `${labelWidth}ch` }}
                  title={l.container}
                >
                  {l.container}
                </span>
              ) : null}
              <span
                className={cn(
                  "min-w-0 flex-1 break-all whitespace-pre-wrap",
                  LEVEL_CLASS[l.level],
                )}
              >
                {l.message}
              </span>
            </div>
          ))}
        </div>
      )}

      {!stickToBottom && shown.length > 0 ? (
        <Button
          size="sm"
          className="absolute right-4 bottom-4 h-7 max-sm:h-10 gap-1 rounded-full bg-console-foreground px-3 text-xs text-console shadow-lg hover:bg-console-foreground/90"
          onClick={jumpToBottom}
        >
          <ArrowDown className="size-3.5" aria-hidden /> Jump to latest
        </Button>
      ) : null}
    </div>
  );
}
