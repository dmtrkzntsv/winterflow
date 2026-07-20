import { Eye, EyeOff } from "lucide-react"
import {
  forwardRef,
  useId,
  useMemo,
  useState,
  type ComponentPropsWithoutRef,
} from "react"

import { cn } from "@/lib/utils"

type PasswordInputProps = ComponentPropsWithoutRef<"input"> & {
  toggleLabel?: string
  hideLabel?: string
}

const PasswordInput = forwardRef<HTMLInputElement, PasswordInputProps>(
  (
    {
      className,
      type,
      toggleLabel = "Show password",
      hideLabel = "Hide password",
      ...props
    },
    ref
  ) => {
    const generatedId = useId()
    const [visible, setVisible] = useState(false)
    const inputType =
      type && type !== "password" && type !== "text" ? type : visible ? "text" : "password"

    const icon = useMemo(() => (visible ? <EyeOff /> : <Eye />), [visible])
    const srLabel = visible ? hideLabel : toggleLabel

    return (
      <div className="relative">
        <input
          id={props.id ?? generatedId}
          type={inputType}
          className={cn(
            "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 pr-10 text-base shadow-sm transition-colors file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
            className
          )}
          ref={ref}
          {...props}
        />
        <button
          type="button"
          className="text-muted-foreground hover:text-foreground absolute inset-y-0 right-2 flex items-center"
          onClick={() => {
            setVisible((prev) => !prev)
          }}
          aria-label={srLabel}
        >
          {icon}
        </button>
      </div>
    )
  }
)
PasswordInput.displayName = "PasswordInput"

export { PasswordInput }
