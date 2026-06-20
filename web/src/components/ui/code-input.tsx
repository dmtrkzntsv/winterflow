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
}

export function CodeInput({
  length = 6,
  value,
  onChange,
  className,
  disabled = false,
  autoFocus = false,
}: CodeInputProps) {
  const normalizedValue = value.toUpperCase()
  const handleChange = (nextValue: string) => {
    onChange(nextValue.toUpperCase())
  }
  const firstSegmentLength = Math.min(3, length)
  const secondSegmentLength = Math.max(length - firstSegmentLength, 0)
  const slotClassName = (slotIndex: number) =>
    cn(
      "h-16 w-14 border border-input bg-background text-2xl font-semibold tracking-widest",
      slotIndex === 0 || slotIndex === length - 1
        ? "rounded-2xl"
        : "rounded-xl"
    )

  return (
    <InputOTP
      maxLength={length}
      value={normalizedValue}
      onChange={handleChange}
      pattern="\d*"
      inputMode="numeric"
      disabled={disabled}
      autoFocus={autoFocus}
      containerClassName={cn(
        "w-full flex items-center justify-center gap-4",
        className
      )}
    >
      <InputOTPGroup className="flex gap-3 px-2">
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
          <InputOTPSeparator className="text-3xl font-bold text-muted-foreground">
            -
          </InputOTPSeparator>

          <InputOTPGroup className="flex gap-3 px-2">
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
