import { useEffect, useState } from "react";
import { Check, Copy } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { copyText } from "@/lib/clipboard";
import { lowerFirst } from "@/lib/utils";

// COPIED_MS is how long the button shows its check mark.
const COPIED_MS = 2000;

// CopyField shows a value handed over once (a temporary password, a new
// token) read-only, with a Copy button; focusing it selects it all.
export function CopyField({ value, label }: { value: string; label: string }) {
  // copied names the value that was copied, so a new value reads uncopied.
  const [copied, setCopied] = useState<string | null>(null);
  useEffect(() => {
    if (copied === null) return;
    const id = window.setTimeout(() => setCopied(null), COPIED_MS);
    return () => window.clearTimeout(id);
  }, [copied]);
  const copy = async () => {
    try {
      await copyText(value);
      setCopied(value);
      toast.success("Copied");
    } catch {
      toast.error("Couldn't copy: select the text and copy it yourself");
    }
  };
  return (
    <div className="flex items-center gap-2">
      <Input
        readOnly
        aria-label={label}
        value={value}
        className="font-mono text-xs"
        onFocus={(e) => e.currentTarget.select()}
      />
      <Button
        type="button"
        variant="outline"
        size="icon"
        className="size-10 shrink-0 sm:size-9"
        aria-label={`Copy ${lowerFirst(label)}`}
        onClick={() => void copy()}
      >
        {copied === value ? <Check className="size-4" /> : <Copy className="size-4" />}
      </Button>
    </div>
  );
}
