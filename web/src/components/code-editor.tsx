import { useMemo } from "react";
import CodeMirror, { EditorView } from "@uiw/react-codemirror";
import { yaml } from "@codemirror/lang-yaml";
import { Prec, type Extension } from "@codemirror/state";

import { useDarkMode } from "@/hooks/use-dark-mode";
import { cn } from "@/lib/utils";

// Surface colours come from the theme tokens so the editor sits on its card
// in both schemes; syntax colours come from the light or dark base theme.
const tokenTheme = Prec.highest(EditorView.theme({
  "&": { backgroundColor: "transparent", color: "var(--card-foreground)" },
  ".cm-content": { caretColor: "var(--foreground)" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--foreground)" },
  ".cm-gutters": {
    backgroundColor: "color-mix(in oklab, var(--muted) 60%, transparent)",
    color: "var(--muted-foreground)",
    borderRight: "1px solid var(--border)",
  },
  ".cm-activeLine": {
    backgroundColor: "color-mix(in oklab, var(--accent) 45%, transparent)",
  },
  ".cm-activeLineGutter": {
    backgroundColor: "color-mix(in oklab, var(--accent) 70%, transparent)",
    color: "var(--foreground)",
  },
  "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection":
    { backgroundColor: "color-mix(in oklab, var(--ring) 30%, transparent)" },
  ".cm-placeholder": { color: "var(--muted-foreground)" },
}));

type Props = {
  value: string;
  onChange?: (value: string) => void;
  // readOnly shows the content without letting it change (an instance's
  // files, owned by its template).
  readOnly?: boolean;
  filename?: string;
  placeholder?: string;
  className?: string;
};

// CodeEditor is a thin CodeMirror 6 wrapper for editing app files. Language
// support is picked by filename extension (YAML for compose files; plain text
// otherwise). Deliberately minimal — no themes bundle, no LSP — to stay light;
// the built-in light/dark base follows the page's colour scheme.
export function CodeEditor({
  value,
  onChange,
  readOnly = false,
  filename,
  placeholder,
  className,
}: Props) {
  const dark = useDarkMode();
  const extensions = useMemo<Extension[]>(() => {
    const name = (filename ?? "").toLowerCase();
    if (name.endsWith(".yml") || name.endsWith(".yaml")) {
      return [yaml(), tokenTheme];
    }
    return [tokenTheme];
  }, [filename]);

  return (
    <div
      className={cn(
        "overflow-hidden rounded-md border bg-card font-mono text-xs [&_.cm-editor.cm-focused]:outline-none",
        className,
      )}
    >
      <CodeMirror
        value={value}
        onChange={onChange}
        readOnly={readOnly}
        editable={!readOnly}
        extensions={extensions}
        theme={dark ? "dark" : "light"}
        placeholder={placeholder}
        minHeight="12rem"
        maxHeight="32rem"
        basicSetup={{
          lineNumbers: true,
          foldGutter: false,
          highlightActiveLine: true,
          autocompletion: false,
          searchKeymap: false,
        }}
      />
    </div>
  );
}
