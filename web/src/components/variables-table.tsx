import { Link } from "react-router-dom";
import { Lock, Pencil, RefreshCw, RotateCw, Trash2 } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  coverageLabel,
  type BuiltinVariable,
  type InheritedVariable,
  type SharedVariable,
} from "@/lib/shared-variables";

export type VariablesTableProps = {
  title: string;
  variables: SharedVariable[];
  inherited?: InheritedVariable[];
  builtins?: BuiltinVariable[];
  serverNames: Map<string, string>;
  // Org page: a Coverage column ("All servers" / "Missing on …").
  showCoverage?: boolean;
  onEdit: (v: SharedVariable) => void;
  onDelete: (v: SharedVariable) => void;
  onReload: (v: SharedVariable) => void;
  onProvide?: (v: SharedVariable) => void;
  onResync?: (v: SharedVariable) => void;
};

// Value never renders a secret: the API only ever sends "<encrypted>".
function Value({ v }: { v: SharedVariable }) {
  if (v.is_secret) {
    return (
      <span className="inline-flex items-center gap-1 text-muted-foreground">
        <Lock className="size-3" /> encrypted
      </span>
    );
  }
  return <code className="break-all text-xs">{v.value}</code>;
}

function Empty({ children }: { children: string }) {
  return <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">{children}</p>;
}

// VariablesTable lists shared variables in up to three groups: the
// editable ones (title), the org's as seen from a server (read-only), and
// the agent's built-ins (read-only). Shared by the server Variables tab and
// the org variables page.
export function VariablesTable({
  title,
  variables,
  inherited,
  builtins,
  serverNames,
  showCoverage,
  onEdit,
  onDelete,
  onReload,
  onProvide,
  onResync,
}: VariablesTableProps) {
  return (
    <div className="space-y-8">
      <section aria-label={title} className="space-y-2">
        <h3 className="text-sm font-medium">{title}</h3>
        {variables.length === 0 ? (
          <Empty>No variables yet.</Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Value</TableHead>
                {showCoverage ? <TableHead>Coverage</TableHead> : null}
                <TableHead>Description</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {variables.map((v) => (
                <TableRow key={v.id}>
                  <TableCell className="font-mono">{v.name}</TableCell>
                  <TableCell>
                    <Value v={v} />
                  </TableCell>
                  {showCoverage ? (
                    <TableCell className={v.missing_on.length > 0 ? "text-amber-700 dark:text-amber-400" : undefined}>
                      {v.is_secret ? coverageLabel(v, serverNames) : "—"}
                    </TableCell>
                  ) : null}
                  <TableCell className="text-muted-foreground">{v.description}</TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      {onProvide && v.is_secret && v.missing_on.length > 0 ? (
                        <Button size="sm" variant="outline" onClick={() => onProvide(v)}>
                          Provide value
                        </Button>
                      ) : null}
                      <Button size="icon" variant="ghost" title="Reload apps using this" aria-label={`Reload apps using ${v.name}`} onClick={() => onReload(v)}>
                        <RotateCw className="size-4" />
                      </Button>
                      {onResync ? (
                        <Button size="icon" variant="ghost" title="Resync" aria-label={`Resync ${v.name}`} onClick={() => onResync(v)}>
                          <RefreshCw className="size-4" />
                        </Button>
                      ) : null}
                      <Button size="icon" variant="ghost" aria-label={`Edit ${v.name}`} onClick={() => onEdit(v)}>
                        <Pencil className="size-4" />
                      </Button>
                      <Button size="icon" variant="ghost" className="hover:text-destructive" aria-label={`Delete ${v.name}`} onClick={() => onDelete(v)}>
                        <Trash2 className="size-4" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </section>

      {inherited ? (
        <section aria-label="Inherited from org" className="space-y-2">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium">Inherited from org</h3>
            <Link to="/org/variables" className="text-sm underline-offset-4 hover:underline">
              Manage org variables
            </Link>
          </div>
          {inherited.length === 0 ? (
            <Empty>The organization has no variables.</Empty>
          ) : (
            <Table>
              <TableBody>
                {inherited.map((v) => {
                  const missing = v.is_secret && v.missing_on.length > 0 && !v.overridden;
                  return (
                    <TableRow key={v.id} className={v.overridden ? "opacity-60" : undefined}>
                      <TableCell className="font-mono">{v.name}</TableCell>
                      <TableCell>
                        <Value v={v} />
                      </TableCell>
                      <TableCell>
                        {v.overridden ? <Badge variant="outline">overridden</Badge> : null}
                        {missing ? <Badge variant="destructive">missing here</Badge> : null}
                      </TableCell>
                      <TableCell className="text-right">
                        {missing && onProvide ? (
                          <Button size="sm" variant="outline" onClick={() => onProvide(v)}>
                            Provide value
                          </Button>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </section>
      ) : null}

      {builtins ? (
        <section aria-label="Built-in" className="space-y-2">
          <h3 className="text-sm font-medium">Built-in</h3>
          <Table>
            <TableBody>
              {builtins.map((b) => (
                <TableRow key={b.name}>
                  <TableCell className="font-mono">{b.name}</TableCell>
                  <TableCell>
                    {b.value ? <code className="text-xs">{b.value}</code> : <span className="text-muted-foreground">set by the agent</span>}
                  </TableCell>
                  <TableCell className="text-muted-foreground">{b.description}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </section>
      ) : null}
    </div>
  );
}
