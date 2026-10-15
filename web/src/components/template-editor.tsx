import type { ReactNode } from "react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { EditorSection } from "@/components/editor-section";
import { FilesSection } from "@/components/files-section";
import { IconPicker } from "@/components/icon-picker";
import type { TemplateDraft } from "@/lib/template-draft";

type Props = {
  draft: TemplateDraft;
  onChange: (next: TemplateDraft) => void;
  // detected is the Detected panel (TemplateDetected), shown after the
  // files it is read from.
  detected?: ReactNode;
};

// TemplateEditor edits a template revision: its name, how it reads in a
// list (icon, colour, summary, description) and the app's files (the app
// editor's file editing, without secrets). What each app fills in, which
// ports it exposes and which backups it can turn on are declared in the
// compose file's x-winterflow block; detected shows them, read-only.
export function TemplateEditor({ draft, onChange, detected }: Props) {
  const set = (patch: Partial<TemplateDraft>) => onChange({ ...draft, ...patch });
  return (
    <div>
      <EditorSection title="Template" description="Lowercase letters, digits and hyphens; unique in the organization.">
        <div className="grid gap-2">
          <Label htmlFor="template-name">Template name</Label>
          <Input
            id="template-name"
            value={draft.name}
            placeholder="my-stack"
            className="font-mono"
            onChange={(e) => set({ name: e.target.value })}
          />
        </div>
      </EditorSection>

      <EditorSection title="Look" description="How the template reads in lists; each app made from it starts with its icon and colour.">
        <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2">
          <Label>Icon</Label>
          <Label htmlFor="template-summary">Summary</Label>
          <Label htmlFor="template-color">Colour</Label>
          <IconPicker value={draft.icon} color={draft.color} name={draft.name} onChange={(icon) => set({ icon })} />
          <Input
            id="template-summary"
            value={draft.summary}
            placeholder="One line about the app"
            onChange={(e) => set({ summary: e.target.value })}
          />
          <Input
            id="template-color"
            type="color"
            className="h-9 w-14 cursor-pointer p-1"
            value={draft.color || "#64748b"}
            onChange={(e) => set({ color: e.target.value })}
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="template-description">Description</Label>
          <Textarea
            id="template-description"
            value={draft.description}
            placeholder="What the app is for and what to know before adding it (optional)"
            rows={3}
            onChange={(e) => set({ description: e.target.value })}
          />
        </div>
      </EditorSection>

      <FilesSection
        files={draft.files}
        contents={draft.contents}
        onChange={(files, contents) => set({ files, contents })}
        allowSecret={false}
        imageChips={false}
        description={
          <>
            Copied unchanged into each app. Declare under <code>x-winterflow</code> what each app fills in (
            <code>variables</code>), the ports it exposes (<code>expose</code>) and its <code>backups</code>; any other{" "}
            <code>{"${VAR}"}</code> must come from a shared variable on the server.
          </>
        }
      />

      {detected}
    </div>
  );
}
