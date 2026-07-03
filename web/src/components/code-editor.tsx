import { useMemo } from "react";
import CodeMirror from "@uiw/react-codemirror";
import { yaml } from "@codemirror/lang-yaml";
import type { Extension } from "@codemirror/state";

type Props = {
  value: string;
  onChange: (value: string) => void;
  filename?: string;
  placeholder?: string;
};

// CodeEditor is a thin CodeMirror 6 wrapper for editing app files. Language
// support is picked by filename extension (YAML for compose files; plain text
// otherwise). Deliberately minimal — no themes bundle, no LSP — to stay light.
export function CodeEditor({ value, onChange, filename, placeholder }: Props) {
  const extensions = useMemo<Extension[]>(() => {
    const name = (filename ?? "").toLowerCase();
    if (name.endsWith(".yml") || name.endsWith(".yaml")) {
      return [yaml()];
    }
    return [];
  }, [filename]);

  return (
    <div className="overflow-hidden rounded-md border font-mono text-xs [&_.cm-editor]:bg-transparent [&_.cm-editor.cm-focused]:outline-none [&_.cm-gutters]:bg-muted/40">
      <CodeMirror
        value={value}
        onChange={onChange}
        extensions={extensions}
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
