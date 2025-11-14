import {
  useMemo,
  type ComponentPropsWithoutRef,
  type CSSProperties,
} from "react"

import { cn } from "@/lib/utils"

type Snowflake = {
  left: number
  delay: number
  duration: number
  size: number
  opacity: number
  drift: number
  initialY: number
  id: number
}

type LoginIllustrationProps = ComponentPropsWithoutRef<"div">
type SnowflakeStyle = CSSProperties & { "--flake-drift": string; "--initial-y": string }
const SNOWFLAKE_COUNT = 34

const createFlakes = (): Snowflake[] =>
  Array.from({ length: SNOWFLAKE_COUNT }, (_, idx) => ({
    left: Math.random() * 100,
    delay: 0,
    duration: 18 + Math.random() * 6,
    size: 18 + Math.random() * 18,
    opacity: 0.6 + Math.random() * 0.4,
    drift: (Math.random() - 0.5) * 20,
    initialY: -60 - (Math.random() * 800),
    id: idx,
  }))

export function LoginIllustration({ className, ...props }: LoginIllustrationProps) {
  const flakes = useMemo(createFlakes, [])

  return (
    <div
      className={cn(
        "bg-gradient-to-br from-[#0f172a] via-[#1d2743] to-[#0f172a] relative overflow-hidden rounded-md",
        "border border-white/10 shadow-inner",
        className,
      )}
      {...props}
    >
      <div className="absolute inset-0 z-0 bg-[radial-gradient(circle_at_20%_20%,rgba(255,255,255,0.25),transparent_45%)]" />
      <div className="absolute inset-0 z-0 bg-[radial-gradient(circle_at_80%_30%,rgba(14,165,233,0.35),transparent_50%)] mix-blend-screen" />
      {flakes.map((flake) => {
        const style: SnowflakeStyle = {
          left: `${flake.left}%`,
          animationDelay: `${flake.delay}s`,
          animationDuration: `${flake.duration.toFixed(2)}s`,
          width: `${flake.size}px`,
          height: `${flake.size}px`,
          opacity: flake.opacity,
          "--flake-drift": `${flake.drift}px`,
          "--initial-y": `${flake.initialY}px`,
        }
        return (
          <span
            key={flake.id}
            className="login-snowflake pointer-events-none"
            aria-hidden="true"
            style={style}
          >
            <svg
              viewBox="0 0 24 24"
              className="h-full w-full text-white drop-shadow-[0_2px_6px_rgba(14,165,233,0.55)]"
            >
              <use href="#winterflow-asterisk" />
            </svg>
          </span>
        )
      })}
      <div className="absolute inset-x-0 bottom-0 z-10 h-24 bg-gradient-to-t from-[#0f172a]/80 to-transparent" />
    </div>
  )
}
