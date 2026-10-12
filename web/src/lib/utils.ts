import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// formatBytes renders a byte count (as string or number) as GB/TB with one
// decimal, for the server hardware specs line. Returns "" for unparseable
// input so missing capabilities render as nothing.
export function formatBytes(value: string | number | undefined): string {
  const n = typeof value === "string" ? Number(value) : value
  if (n === undefined || !Number.isFinite(n) || n <= 0) return ""
  const gb = n / 1024 ** 3
  if (gb >= 1024) return `${(gb / 1024).toFixed(1)} TB`
  return `${gb.toFixed(1)} GB`
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" })

// relativeTime renders "5 minutes ago"-style labels (now in ms), falling back
// to the date once the time is more than a week away.
export function relativeTime(d: Date, now: number = Date.now()): string {
  const secs = Math.round((d.getTime() - now) / 1000)
  const abs = Math.abs(secs)
  if (abs < 60) return rtf.format(secs, "second")
  if (abs < 3600) return rtf.format(Math.round(secs / 60), "minute")
  if (abs < 86400) return rtf.format(Math.round(secs / 3600), "hour")
  if (abs < 7 * 86400) return rtf.format(Math.round(secs / 86400), "day")
  return d.toLocaleDateString()
}
