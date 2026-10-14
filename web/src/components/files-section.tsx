import type { ReactNode } from "react";
import { FileText, Lock, LockOpen, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { CodeEditor } from "@/components/code-editor";
import { EditorSection } from "@/components/editor-section";
import { FieldMessage } from "@/components/form-field";
import { ImageTagPicker } from "@/components/image-tag-picker";
import { EmptyNote } from "@/components/page-states";
import { imageRefs } from "@/lib/compose-images";
import { addFile, removeFile } from "@/lib/editor-rows";
import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import { cn } from "@/lib/utils";
import { isComposeFile, type AppFileMeta } from "@/types/app-config";

type FileEditorsProps = {
  files: AppFileMeta[];
  contents: Record<string, string>; // file id -> content
  onChange: (files: AppFileMeta[], contents: Record<string, string>) => void;
  // allowSecret offers the per-file secret toggle (an app's files; a
  // template holds no secret).
  allowSecret?: boolean;
  // imageChips offers the image tag browser under compose files (it asks
  // the active server's registries).
  imageChips?: boolean;
  // errors holds file:<id> messages (editorErrors).
  errors?: FieldErrors;
  // addLabel shows a link-style button under the files that adds one.
  addLabel?: string;
};

// FileEditors are the app's files, each a name over a code editor. The
// files section, the compose create form and the template editor share it.
export function FileEditors({
  files,
  contents,
  onChange,
  allowSecret = true,
  imageChips = true,
  errors = {},
  addLabel,
}: FileEditorsProps) {
  const updateMeta = (id: string, patch: Partial<AppFileMeta>) =>
    onChange(
      files.map((f) => (f.id === id ? { ...f, ...patch } : f)),
      contents,
    );
  const updateContent = (id: string, content: string) => onChange(files, { ...contents, [id]: content });
  const add = () => {
    const next = addFile(files, contents);
    onChange(next.files, next.contents);
  };
  const remove = (id: string) => {
    const next = removeFile(files, contents, id);
    onChange(next.files, next.contents);
  };

  return (
    <div className="space-y-3">
      {files.map((f) => {
        const nameId = `file-${f.id}`;
        const error = errors[`file:${f.id}`];
        return (
          <div key={f.id} className="overflow-hidden rounded-md border">
            <div className="flex items-center gap-1 border-b bg-muted/40 py-1 pr-1 pl-1">
              <FileText className="ml-2 size-4 shrink-0 text-muted-foreground" />
              <Input
                id={nameId}
                aria-label="File name"
                value={f.filename}
                placeholder="compose.yml"
                {...invalidAttrs(
                  nameId,
                  error,
                  "h-8 border-transparent bg-transparent font-mono shadow-none focus-visible:border-input focus-visible:bg-background",
                )}
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
            <FieldMessage id={nameId} message={error} className="border-b px-3 py-1.5" />
            <CodeEditor
              value={contents[f.id] ?? ""}
              onChange={(content) => updateContent(f.id, content)}
              filename={f.filename}
              placeholder="file contents (use ${VAR}, including org/server variables)"
              className="rounded-none border-0"
            />
            {imageChips && isComposeFile(f.filename) ? (
              <ImageChips
                content={contents[f.id] ?? ""}
                onReplace={(oldRef, newRef) => updateContent(f.id, (contents[f.id] ?? "").split(oldRef).join(newRef))}
              />
            ) : null}
          </div>
        );
      })}
      {addLabel ? (
        <Button type="button" variant="link" size="sm" className="h-auto px-0" onClick={add}>
          <Plus className="size-4" /> {addLabel}
        </Button>
      ) : null}
    </div>
  );
}

type Props = Omit<FileEditorsProps, "addLabel"> & {
  description: ReactNode;
  // emptyNote stands in for no files (default: add a compose file).
  emptyNote?: ReactNode;
};

// FilesSection is the editor row of files: an explanation, "Add file", and
// the files. errors.files (no compose file) stands in for the empty note.
export function FilesSection({
  files,
  contents,
  onChange,
  description,
  emptyNote,
  allowSecret = true,
  imageChips = true,
  errors = {},
}: Props) {
  const add = () => {
    const next = addFile(files, contents);
    onChange(next.files, next.contents);
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
      {errors.files ? (
        <FieldMessage id="files" message={errors.files} className="text-sm" />
      ) : files.length === 0 ? (
        <EmptyNote>
          {emptyNote ?? (
            <>
              Add at least a <code>compose.yml</code>.
            </>
          )}
        </EmptyNote>
      ) : null}
      {files.length > 0 ? (
        <FileEditors
          files={files}
          contents={contents}
          onChange={onChange}
          allowSecret={allowSecret}
          imageChips={imageChips}
          errors={errors}
        />
      ) : null}
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
          ? "bg-status-warn/10 text-foreground hover:bg-status-warn/15 hover:text-foreground [&>svg]:text-status-warn"
          : "text-muted-foreground",
      )}
      onClick={() => onChange(!on)}
    >
      {on ? <Lock className="size-3.5" /> : <LockOpen className="size-3.5" />}
      <span className="sr-only sm:not-sr-only">Secret</span>
    </Button>
  );
}

// ImageChips lists the image references found in a compose file, each with
// a tag browser that rewrites the reference in place.
export function ImageChips({ content, onReplace }: { content: string; onReplace: (oldRef: string, newRef: string) => void }) {
  const refs = imageRefs(content);
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
