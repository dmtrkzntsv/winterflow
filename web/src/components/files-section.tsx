import type { ReactNode } from "react";
import { FileText, Lock, LockOpen, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { CodeEditor } from "@/components/code-editor";
import { EditorSection } from "@/components/editor-section";
import { ImageTagPicker } from "@/components/image-tag-picker";
import { EmptyNote } from "@/components/page-states";
import { cn } from "@/lib/utils";
import { COMPOSE_FILENAMES, localId, type AppFileMeta } from "@/types/app-config";

type Props = {
  files: AppFileMeta[];
  contents: Record<string, string>; // file id -> content
  onChange: (files: AppFileMeta[], contents: Record<string, string>) => void;
  description: ReactNode;
  // allowSecret offers the per-file secret toggle (an app's files; a
  // template holds no secret).
  allowSecret?: boolean;
  // imageChips offers the image tag browser under compose files (it asks
  // the active server's registries).
  imageChips?: boolean;
};

// FilesSection is the app editor's file editing: named files, each in a
// code editor, added and removed freely. The app editor and the template
// editor share it.
export function FilesSection({ files, contents, onChange, description, allowSecret = true, imageChips = true }: Props) {
  const updateMeta = (id: string, patch: Partial<AppFileMeta>) =>
    onChange(
      files.map((f) => (f.id === id ? { ...f, ...patch } : f)),
      contents,
    );
  const updateContent = (id: string, content: string) => onChange(files, { ...contents, [id]: content });
  const add = () => {
    const id = localId("file");
    onChange([...files, { id, filename: "", is_encrypted: false }], { ...contents, [id]: "" });
  };
  const remove = (id: string) => {
    const rest = { ...contents };
    delete rest[id];
    onChange(
      files.filter((f) => f.id !== id),
      rest,
    );
  };

  return (
    <EditorSection
      title="Files"
      description={description}
      action={
        <Button size="sm" variant="outline" onClick={add}>
          <Plus className="size-4" /> Add file
        </Button>
      }
    >
      {files.length === 0 ? (
        <EmptyNote>
          Add at least a <code>compose.yml</code>.
        </EmptyNote>
      ) : null}
      {files.map((f) => (
        <div key={f.id} className="overflow-hidden rounded-md border">
          <div className="flex items-center gap-1 border-b bg-muted/40 py-1 pr-1 pl-1">
            <FileText className="ml-2 size-4 shrink-0 text-muted-foreground" />
            <Input
              aria-label="File name"
              value={f.filename}
              placeholder="compose.yml"
              className="h-8 border-transparent bg-transparent font-mono shadow-none focus-visible:border-input focus-visible:bg-background"
              onChange={(e) => updateMeta(f.id, { filename: e.target.value })}
            />
            {allowSecret ? (
              <SecretToggle on={f.is_encrypted} onChange={(on) => updateMeta(f.id, { is_encrypted: on })} />
            ) : null}
            <Button
              size="icon"
              variant="ghost"
              className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
              aria-label={`Remove ${f.filename || "file"}`}
              onClick={() => remove(f.id)}
            >
              <Trash2 className="size-4" />
            </Button>
          </div>
          <CodeEditor
            value={contents[f.id] ?? ""}
            onChange={(content) => updateContent(f.id, content)}
            filename={f.filename}
            placeholder="file contents (use ${VAR}, including org/server variables)"
            className="rounded-none border-0"
          />
          {imageChips && isComposeFilename(f.filename) ? (
            <ImageChips
              content={contents[f.id] ?? ""}
              onReplace={(oldRef, newRef) => updateContent(f.id, (contents[f.id] ?? "").split(oldRef).join(newRef))}
            />
          ) : null}
        </div>
      ))}
    </EditorSection>
  );
}

// SecretToggle marks a file or variable as secret (encrypted end to end).
export function SecretToggle({ on, onChange }: { on: boolean; onChange: (on: boolean) => void }) {
  return (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      aria-pressed={on}
      title={on ? "Secret: encrypted for this server" : "Mark as secret"}
      className={cn(
        "h-8 shrink-0 gap-1.5 px-2 text-xs",
        on
          ? "bg-amber-500/10 text-amber-700 hover:bg-amber-500/15 hover:text-amber-800 dark:text-amber-400"
          : "text-muted-foreground",
      )}
      onClick={() => onChange(!on)}
    >
      {on ? <Lock className="size-3.5" /> : <LockOpen className="size-3.5" />}
      <span className="sr-only sm:not-sr-only">Secret</span>
    </Button>
  );
}

// isComposeFilename gates the image-tag chips to compose files.
function isComposeFilename(name: string): boolean {
  return COMPOSE_FILENAMES.includes(name.trim());
}

const IMAGE_LINE = /^\s*image:\s*["']?([\w][\w./:@-]*)/gm;

// ImageChips lists the image references found in a compose file, each with a
// tag browser that rewrites the reference in place.
function ImageChips({ content, onReplace }: { content: string; onReplace: (oldRef: string, newRef: string) => void }) {
  const refs = Array.from(new Set(Array.from(content.matchAll(IMAGE_LINE), (m) => m[1]).filter(Boolean)));
  if (refs.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-2 border-t bg-muted/20 px-3 py-2">
      <span className="text-xs text-muted-foreground">Images</span>
      {refs.map((ref) => (
        <ImageTagPicker key={ref} image={ref} onSelect={(newRef) => onReplace(ref, newRef)} />
      ))}
    </div>
  );
}
