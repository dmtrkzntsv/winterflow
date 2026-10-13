import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";

import { Input } from "@/components/ui/input";
import { HostHints, Suggestions, type HostCheck } from "@/components/host-hints";
import { checkHostname } from "@/lib/host-check";
import { validHostname, type RegisteredDomain } from "@/lib/domains";
import { cn } from "@/lib/utils";

type Props = {
  rowId: string;
  value: string;
  label: string;
  placeholder: string;
  // rowClassName lays out the input and the row's other controls (children).
  rowClassName: string;
  inputClassName?: string;
  // checked: the hostname claims itself (a domain, or a whole-domain
  // redirect), so it gets the live check, suggestions and hints. A path
  // rule rides a hostname checked on another row.
  checked: boolean;
  serverId: string;
  appId?: string;
  registrations: RegisteredDomain[];
  onChange: (value: string) => void;
  // onRegistration reports the covering registration of a hostname the
  // user edited.
  onRegistration: (reg: RegisteredDomain) => void;
  // onReturn runs when the user comes back to the tab while the hostname is
  // flagged as uncovered (they may have registered a domain meanwhile).
  onReturn: () => void;
  children: ReactNode;
};

// HostnameField is one ingress row (a domain or a redirect): the hostname
// input beside the row's other controls, and under them the live check's
// verdict, or suggestions while the hostname is empty. The check state is
// the row's own, so removing the row drops it with its timer.
export function HostnameField({
  rowId,
  value,
  label,
  placeholder,
  rowClassName,
  inputClassName,
  checked,
  serverId,
  appId,
  registrations,
  onChange,
  onRegistration,
  onReturn,
  children,
}: Props) {
  // The last answer, with the hostname it was for: once the text changes it
  // no longer shows, with nothing extra to clear.
  const [result, setResult] = useState<{ host: string; check: HostCheck } | null>(null);
  // Bumped to check the same hostname again (see the focus handler).
  const [rechecks, setRechecks] = useState(0);
  // Whether the user changed the hostname: loaded rows are checked at once
  // and keep their stored values; edits are debounced and report back.
  const edited = useRef(false);
  const input = useRef<HTMLInputElement>(null);
  const reportRegistration = useRef(onRegistration);
  useEffect(() => {
    reportRegistration.current = onRegistration;
  });

  useEffect(() => {
    // Bad syntax never reaches the API: the save-time validation says why.
    if (!checked || !validHostname(value)) return;
    let live = true;
    const timer = setTimeout(
      async () => {
        try {
          const check = await checkHostname(serverId, appId, value);
          if (!live || !check) return;
          setResult({ host: value, check });
          if (edited.current && check.registration) reportRegistration.current(check.registration);
        } catch {
          // Advisory; the save-time check is authoritative.
        }
      },
      edited.current ? 400 : 0,
    );
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [checked, value, serverId, appId, rechecks]);

  const shown = checked && result?.host === value ? result.check : undefined;
  const issue = shown?.issue;

  // An "uncovered" hint links to the Domains tab in a new tab. When the user
  // comes back, re-check so a domain registered meanwhile clears the hint.
  // Only while flagged: a plain focus change costs nothing. The listener
  // stays attached for the row's life and reads the flag from a ref set at
  // commit: attaching it in a passive effect keyed on the flag leaves a gap
  // after the hint is painted in which a return would be lost, and the hint
  // would never clear.
  const uncovered = issue?.kind === "uncovered";
  const flagged = useRef(false);
  useLayoutEffect(() => {
    flagged.current = uncovered;
  });
  useEffect(() => {
    const onFocus = () => {
      if (!flagged.current) return;
      onReturn();
      setRechecks((n) => n + 1);
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [onReturn]);

  const edit = (next: string) => {
    edited.current = true;
    onChange(next);
  };

  // pick fills the hostname from a suggestion; a wildcard's ".suffix" gets
  // the caret at its start so the label can be typed straight away.
  const pick = (next: string, caretAtStart: boolean) => {
    edit(next);
    requestAnimationFrame(() => {
      const el = input.current;
      if (!el) return;
      el.focus();
      if (caretAtStart) el.setSelectionRange(0, 0);
    });
  };

  const hintId = `${rowId}-hint`;
  return (
    <div className="rounded-md border p-3">
      <div className={rowClassName}>
        <Input
          ref={input}
          value={value}
          placeholder={placeholder}
          aria-label={label}
          aria-invalid={issue !== undefined}
          aria-describedby={issue || shown?.registration ? hintId : undefined}
          className={cn(
            "font-mono",
            inputClassName,
            issue && "border-destructive focus-visible:ring-destructive",
          )}
          onChange={(e) => edit(e.target.value.trim().toLowerCase())}
        />
        {children}
      </div>
      {!checked ? null : value === "" ? (
        <Suggestions registrations={registrations} onPick={pick} />
      ) : shown ? (
        <HostHints id={hintId} check={shown} />
      ) : null}
    </div>
  );
}
