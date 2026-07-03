import { Eye, EyeOff, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { CodeEditor } from "@/components/code-editor";
import { IconPicker } from "@/components/icon-picker";
import { useState } from "react";
import {
  localId,
  type AppEditorState,
  type AppFileMeta,
  type AppVariableMeta,
} from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
};

// AppEditor edits an app's compose files and variables (v1 parity, minus
// extensions). Secrets are flagged with is_encrypted and encrypted on submit by
// the page; the editor only manages plaintext entry + masking.
export function AppEditor({ state, onChange }: Props) {
  const [revealed, setRevealed] = useState<Record<string, boolean>>({});

  const setConfig = (patch: Partial<AppEditorState["config"]>) =>
    onChange({ ...state, config: { ...state.config, ...patch } });

  // --- files ---
  const updateFileMeta = (id: string, patch: Partial<AppFileMeta>) =>
    setConfig({
      files: state.config.files.map((f) =>
        f.id === id ? { ...f, ...patch } : f,
      ),
    });

  const updateFileContent = (id: string, content: string) =>
    onChange({ ...state, files: { ...state.files, [id]: content } });

  const addFile = () => {
    const id = localId("file");
    onChange({
      ...state,
      config: {
        ...state.config,
        files: [
          ...state.config.files,
          { id, filename: "", is_encrypted: false },
        ],
      },
      files: { ...state.files, [id]: "" },
    });
  };

  const removeFile = (id: string) => {
    const rest = { ...state.files };
    delete rest[id];
    onChange({
      ...state,
      config: {
        ...state.config,
        files: state.config.files.filter((f) => f.id !== id),
      },
      files: rest,
    });
  };

  // --- variables ---
  const updateVarMeta = (id: string, patch: Partial<AppVariableMeta>) =>
    setConfig({
      variables: state.config.variables.map((v) =>
        v.id === id ? { ...v, ...patch } : v,
      ),
    });

  const updateVarValue = (id: string, value: string) =>
    onChange({ ...state, variables: { ...state.variables, [id]: value } });

  const addVar = () => {
    const id = localId("var");
    onChange({
      ...state,
      config: {
        ...state.config,
        variables: [
          ...state.config.variables,
          { id, name: "", is_encrypted: false },
        ],
      },
      variables: { ...state.variables, [id]: "" },
    });
  };

  const removeVar = (id: string) => {
    const rest = { ...state.variables };
    delete rest[id];
    onChange({
      ...state,
      config: {
        ...state.config,
        variables: state.config.variables.filter((v) => v.id !== id),
      },
      variables: rest,
    });
  };

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Details</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2">
          <div className="flex items-end gap-3 sm:col-span-2">
            <div className="grid gap-2">
              <Label>Icon</Label>
              <IconPicker
                value={state.config.icon}
                color={state.config.color}
                onChange={(icon) => setConfig({ icon })}
              />
            </div>
            <div className="grid flex-1 gap-2">
              <Label htmlFor="app-name">Name</Label>
              <Input
                id="app-name"
                value={state.config.name}
                placeholder="my-app"
                onChange={(e) => setConfig({ name: e.target.value })}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="app-color">Color</Label>
              <Input
                id="app-color"
                type="color"
                className="h-9 w-16 p-1"
                value={state.config.color || "#64748b"}
                onChange={(e) => setConfig({ color: e.target.value })}
              />
            </div>
          </div>
          <div className="grid gap-2 sm:col-span-2">
            <Label htmlFor="app-desc">Description</Label>
            <Input
              id="app-desc"
              value={state.config.description || ""}
              placeholder="Optional"
              onChange={(e) => setConfig({ description: e.target.value })}
            />
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <CardTitle>Files</CardTitle>
          <Button size="sm" variant="outline" onClick={addFile}>
            <Plus className="size-4" /> Add file
          </Button>
        </CardHeader>
        <CardContent className="space-y-4">
          {state.config.files.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Add at least a <code>compose.yml</code>.
            </p>
          ) : null}
          {state.config.files.map((f) => (
            <div key={f.id} className="rounded-md border p-3">
              <div className="mb-2 flex items-center gap-2">
                <Input
                  value={f.filename}
                  placeholder="compose.yml"
                  className="font-mono"
                  onChange={(e) =>
                    updateFileMeta(f.id, { filename: e.target.value })
                  }
                />
                <label className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                  <Checkbox
                    checked={f.is_encrypted}
                    onCheckedChange={(c) =>
                      updateFileMeta(f.id, { is_encrypted: c === true })
                    }
                  />
                  Secret
                </label>
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8 shrink-0"
                  onClick={() => removeFile(f.id)}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
              <CodeEditor
                value={state.files[f.id] ?? ""}
                onChange={(content) => updateFileContent(f.id, content)}
                filename={f.filename}
                placeholder="file contents (use ${VAR} for variables)"
              />
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <CardTitle>Variables</CardTitle>
          <Button size="sm" variant="outline" onClick={addVar}>
            <Plus className="size-4" /> Add variable
          </Button>
        </CardHeader>
        <CardContent className="space-y-3">
          {state.config.variables.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Variables are substituted into files as <code>{"${NAME}"}</code>.
            </p>
          ) : null}
          {state.config.variables.map((v) => {
            const isRevealed = revealed[v.id] || !v.is_encrypted;
            return (
              <div key={v.id} className="flex items-center gap-2">
                <Input
                  value={v.name}
                  placeholder="VAR_NAME"
                  className="font-mono sm:max-w-56"
                  onChange={(e) => updateVarMeta(v.id, { name: e.target.value })}
                />
                <Input
                  value={state.variables[v.id] ?? ""}
                  type={isRevealed ? "text" : "password"}
                  placeholder="value"
                  onChange={(e) => updateVarValue(v.id, e.target.value)}
                />
                {v.is_encrypted ? (
                  <Button
                    size="icon"
                    variant="ghost"
                    className="size-8 shrink-0"
                    onClick={() =>
                      setRevealed((r) => ({ ...r, [v.id]: !r[v.id] }))
                    }
                  >
                    {isRevealed ? (
                      <EyeOff className="size-4" />
                    ) : (
                      <Eye className="size-4" />
                    )}
                  </Button>
                ) : null}
                <label className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                  <Checkbox
                    checked={v.is_encrypted}
                    onCheckedChange={(c) =>
                      updateVarMeta(v.id, { is_encrypted: c === true })
                    }
                  />
                  Secret
                </label>
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8 shrink-0"
                  onClick={() => removeVar(v.id)}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
            );
          })}
        </CardContent>
      </Card>
    </div>
  );
}
