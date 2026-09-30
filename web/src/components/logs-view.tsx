import { useEffect, useMemo, useRef, useState } from "react";
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
  4: "text-amber-300",
  5: "text-red-400",
  6: "text-red-400 font-semibold",
};

// Container name colors, in the spirit of `docker compose logs`: each service
// keeps one hue so interleaved output stays easy to follow.
const CONTAINER_COLORS = [
  "text-sky-400",
  "text-emerald-400",
  "text-violet-400",
  "text-amber-400",
  "text-pink-400",
  "text-teal-300",
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

// LogsView is a console-style log display: a scroll box that sticks to the
// bottom while new lines arrive, unless the user has scrolled up to read
// (then it stays put until they jump back down).
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

  useEffect(() => {
    const el = scrollRef.current;
    if (el && stickToBottom) {
      el.scrollTop = el.scrollHeight;
    }
  }, [lines, stickToBottom]);

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
    <div className="relative flex h-[calc(100svh-16rem)] min-h-80 flex-col overflow-hidden rounded-lg border border-zinc-800 bg-zinc-950 text-zinc-300">
      <div className="flex items-center gap-3 border-b border-zinc-800 px-3 py-2">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-4 gap-y-1 text-xs">
          {containers.length === 0 ? (
            <span className="text-zinc-500">Container output</span>
          ) : (
            containers.map((c) => (
              <span key={c} className="flex items-center gap-1.5 font-mono">
                <span
                  className={cn("size-2 rounded-full bg-current", colorOf(c))}
                  aria-hidden
                />
                <span className="text-zinc-400">{c}</span>
              </span>
            ))
          )}
        </div>
        <Select
          value={String(tail)}
          onValueChange={(v) => onTailChange(Number(v))}
        >
          <SelectTrigger
            aria-label="Number of lines"
            className="h-7 w-36 border-zinc-800 bg-zinc-900 text-xs text-zinc-300"
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
          className="size-7 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100"
          onClick={onRefresh}
          disabled={loading}
        >
          <RefreshCw className={cn("size-3.5", loading && "animate-spin")} />
        </Button>
      </div>

      {loading && lines.length === 0 ? (
        <div className="flex flex-1 items-center justify-center">
          <Spinner />
        </div>
      ) : error ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
          <p className="text-sm text-red-400">{error}</p>
          <Button
            size="sm"
            variant="outline"
            className="border-zinc-700 bg-transparent text-zinc-200 hover:bg-zinc-800"
            onClick={onRefresh}
          >
            Try again
          </Button>
        </div>
      ) : lines.length === 0 ? (
        <div className="flex flex-1 items-center justify-center p-6 text-sm text-zinc-500">
          No output yet. Start the app, then refresh.
        </div>
      ) : (
        <div
          ref={scrollRef}
          onScroll={handleScroll}
          className="flex-1 overflow-y-auto py-2 font-mono text-xs leading-5"
        >
          {lines.map((l, i) => (
            <div
              key={i}
              className="flex gap-3 px-3 hover:bg-zinc-900"
            >
              <span className="shrink-0 text-zinc-600 tabular-nums select-none">
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

      {!stickToBottom && lines.length > 0 ? (
        <Button
          size="sm"
          className="absolute right-4 bottom-4 h-7 gap-1 rounded-full bg-zinc-100 px-3 text-xs text-zinc-900 shadow-lg hover:bg-white"
          onClick={jumpToBottom}
        >
          <ArrowDown className="size-3.5" /> Latest
        </Button>
      ) : null}
    </div>
  );
}
