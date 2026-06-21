import { useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  MoreVertical,
  Play,
  Square,
  RotateCw,
  ArrowUpCircle,
  ScrollText,
  Pencil,
  SquarePen,
  Trash2,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "sonner";
import { useApps } from "@/context/use-apps";
import type { App, ControlAction } from "@/context/apps-context-base";

export function AppActions({
  app,
  onShowLogs,
}: {
  app: App;
  onShowLogs: (app: App) => void;
}) {
  const navigate = useNavigate();
  const { control, remove, rename } = useApps();
  const [busy, setBusy] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [newName, setNewName] = useState(app.name);

  const run = async (label: string, fn: () => Promise<void>) => {
    setBusy(true);
    try {
      await fn();
      toast.success(`${label} succeeded`);
    } catch (e) {
      toast.error(`${label} failed`, {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const doControl = (action: ControlAction, label: string) =>
    run(label, () => control(app.id, action));

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon"
            className="size-8"
            disabled={busy}
            onClick={(e) => e.stopPropagation()}
          >
            <MoreVertical className="size-4" />
            <span className="sr-only">App actions</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          onClick={(e) => e.stopPropagation()}
        >
          <DropdownMenuItem onSelect={() => void doControl("start", "Start")}>
            <Play className="size-4" /> Start
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => void doControl("stop", "Stop")}>
            <Square className="size-4" /> Stop
          </DropdownMenuItem>
          <DropdownMenuItem
            onSelect={() => void doControl("restart", "Restart")}
          >
            <RotateCw className="size-4" /> Restart
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => void doControl("update", "Update")}>
            <ArrowUpCircle className="size-4" /> Update
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onSelect={() => navigate(`/apps/${app.id}/edit`)}
          >
            <SquarePen className="size-4" /> Edit
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => onShowLogs(app)}>
            <ScrollText className="size-4" /> Logs
          </DropdownMenuItem>
          <DropdownMenuItem
            onSelect={() => {
              setNewName(app.name);
              setRenameOpen(true);
            }}
          >
            <Pencil className="size-4" /> Rename
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            className="text-destructive focus:text-destructive"
            onSelect={() => setDeleteOpen(true)}
          >
            <Trash2 className="size-4" /> Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={renameOpen} onOpenChange={setRenameOpen}>
        <DialogContent onClick={(e) => e.stopPropagation()}>
          <DialogHeader>
            <DialogTitle>Rename app</DialogTitle>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="app-name">Name</Label>
            <Input
              id="app-name"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRenameOpen(false)}>
              Cancel
            </Button>
            <Button
              disabled={busy || !newName.trim() || newName === app.name}
              onClick={() => {
                setRenameOpen(false);
                void run("Rename", () => rename(app.id, newName.trim()));
              }}
            >
              Save
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent onClick={(e) => e.stopPropagation()}>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {app.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              This stops the app's containers and removes its deployment and
              stored revisions. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => void run("Delete", () => remove(app.id))}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
