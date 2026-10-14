import { REGEXP_ONLY_DIGITS_AND_CHARS } from "input-otp"

import {
  InputOTP,
  InputOTPGroup,
  InputOTPSeparator,
  InputOTPSlot,
} from "@/components/ui/input-otp"
import { cn } from "@/lib/utils"

type CodeInputProps = {
  length?: number
  value: string
  onChange: (value: string) => void
  className?: string
  disabled?: boolean
  autoFocus?: boolean
  // id names the input for a <label htmlFor>.
  id?: string
}

// CodeInput is a pairing-code field: letters and digits (codes are A–Z0–9),
// a text keyboard with capitals on phones, the value uppercased, split
// 3 + 3 into slots that fit a 320px phone (36px slots below 360px).
export function CodeInput({
  length = 6,
  value,
  onChange,
  className,
  disabled = false,
  autoFocus = false,
  id,
}: CodeInputProps) {
  const normalizedValue = value.toUpperCase()
  const handleChange = (nextValue: string) => {
    onChange(nextValue.toUpperCase())
  }
  const firstSegmentLength = Math.min(3, length)
  const secondSegmentLength = Math.max(length - firstSegmentLength, 0)
  const slotClassName = (slotIndex: number) =>
    cn(
      "h-12 w-9 border border-input min-[360px]:w-10 bg-background text-xl font-semibold tracking-widest sm:h-16 sm:w-14 sm:text-2xl",
      slotIndex === 0 || slotIndex === length - 1
        ? "rounded-xl sm:rounded-2xl"
        : "rounded-lg sm:rounded-xl"
    )

  return (
    <InputOTP
      id={id}
      maxLength={length}
      value={normalizedValue}
      onChange={handleChange}
      pattern={REGEXP_ONLY_DIGITS_AND_CHARS}
      inputMode="text"
      autoCapitalize="characters"
      autoCorrect="off"
      spellCheck={false}
      // A pairing code is no password: skip input-otp's password-manager
      // badge handling (it polls the layout every second while mounted).
      pushPasswordManagerStrategy="none"
      disabled={disabled}
      autoFocus={autoFocus}
      containerClassName={cn(
        "w-full flex items-center justify-center gap-1.5 min-[360px]:gap-2 sm:gap-4",
        className
      )}
    >
      <InputOTPGroup className="flex gap-1.5 min-[360px]:gap-2 sm:gap-3 sm:px-2">
        {Array.from({ length: firstSegmentLength }).map((_, index) => (
          <InputOTPSlot
            key={`code-slot-first-${index}`}
            index={index}
            className={slotClassName(index)}
          />
        ))}
      </InputOTPGroup>

      {secondSegmentLength > 0 && (
        <>
          <InputOTPSeparator className="text-2xl font-bold text-muted-foreground sm:text-3xl">
            -
          </InputOTPSeparator>

          <InputOTPGroup className="flex gap-1.5 min-[360px]:gap-2 sm:gap-3 sm:px-2">
            {Array.from({ length: secondSegmentLength }).map((_, index) => {
              const overallIndex = firstSegmentLength + index
              return (
                <InputOTPSlot
                  key={`code-slot-second-${index}`}
                  index={overallIndex}
                  className={slotClassName(overallIndex)}
                />
              )
            })}
          </InputOTPGroup>
        </>
      )}
    </InputOTP>
  )
}
