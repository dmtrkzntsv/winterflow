import { useMemo, useRef, useState } from "react";

import { useCreateApp } from "@/hooks/use-create-app";
import { editorErrors } from "@/lib/app-editor-io";
import { focusFirstInvalid, type FieldErrors } from "@/lib/field-errors";
import type { AppEditorState } from "@/types/app-config";

const NO_ERRORS: FieldErrors = {};

// useCreateForm is the submit sequence of the Compose and Git forms: the
// first Create marks every problem of ready (the state Create would send)
// next to its field, calls onRefused so the form can open the sections
// holding one, focuses the first bad field (attach form to the form's
// root) and sends nothing (and drops the last attempt's failure); a clean Create saves the app. Messages appear
// only after the first attempt, then follow every edit.
export function useCreateForm(serverId: string, ready: AppEditorState) {
  const { create, busy, error, dismiss } = useCreateApp(serverId);
  const [tried, setTried] = useState(false);
  const form = useRef<HTMLDivElement>(null);
  const errors = useMemo(() => (tried ? editorErrors(ready) : NO_ERRORS), [tried, ready]);

  const submit = (onRefused?: (found: FieldErrors) => void) => {
    setTried(true);
    const found = editorErrors(ready);
    if (Object.keys(found).length > 0) {
      // The last attempt's failure no longer describes this form.
      dismiss();
      onRefused?.(found);
      focusFirstInvalid(form.current);
      return;
    }
    void create(ready);
  };

  return { form, errors, invalid: Object.keys(errors).length > 0, busy, error, submit };
}
