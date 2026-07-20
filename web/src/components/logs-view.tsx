import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

export type LogLine = {
  timestamp: number;
  level: number;
  message: string;
  container?: string;
};

const LEVEL_CLASS: Record<number, string> = {
  4: "text-yellow-500",
  5: "text-red-500",
  6: "text-red-600 font-semibold",
};

const TAIL_OPTIONS = [200, 500, 1000] as const;

type Props = {
  lines: LogLine[];
  loading: boolean;
  error: string | null;
  tail: number;
  onTailChange: (tail: number) => void;
  onRefresh: () => void;
};

// LogsView is a deliberately small log display: a monospace scroll box that
// sticks to the bottom while new lines arrive, unless the user has scrolled
// up to read (then it stays put until they return to the bottom).
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

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle>Logs</CardTitle>
        <div className="flex items-center gap-2">
          <Select
            value={String(tail)}
            onValueChange={(v) => onTailChange(Number(v))}
          >
            <SelectTrigger className="h-8 w-28">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {TAIL_OPTIONS.map((n) => (
                <SelectItem key={n} value={String(n)}>
                  Last {n}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button size="sm" variant="outline" onClick={onRefresh} disabled={loading}>
            Refresh
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {loading && lines.length === 0 ? (
          <div className="flex h-40 items-center justify-center">
            <Spinner />
          </div>
        ) : error ? (
          <p className="text-sm text-destructive">{error}</p>
        ) : lines.length === 0 ? (
          <p className="text-sm text-muted-foreground">No logs.</p>
        ) : (
          <div
            ref={scrollRef}
            onScroll={handleScroll}
            className="h-96 overflow-y-auto rounded-md border bg-muted/30 p-3 font-mono text-xs"
          >
            {lines.map((l, i) => (
              <div key={i} className="whitespace-pre-wrap break-all">
                {l.container ? (
                  <span className="text-muted-foreground">{l.container} </span>
                ) : null}
                <span className={LEVEL_CLASS[l.level] ?? ""}>{l.message}</span>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
