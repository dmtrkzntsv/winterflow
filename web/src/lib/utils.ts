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
