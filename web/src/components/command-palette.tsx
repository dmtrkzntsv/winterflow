import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Server } from "lucide-react";

import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { isStandalone } from "@/config";
import { useApps } from "@/context/use-apps";
import { useCommandPalette } from "@/context/use-palette";
import { useServers } from "@/context/use-servers";
import { useSettingsSections } from "@/hooks/use-settings-sections";
import { useThemePreference } from "@/hooks/use-theme";
import {
  paletteNavEntries,
  pushRecent,
  readRecent,
  visibleRecent,
  type PaletteGroup,
  type PaletteNavEntry,
} from "@/lib/palette";
import { THEME_OPTIONS } from "@/lib/theme-options";

const NAV_GROUPS: PaletteGroup[] = ["Apps", "Pages", "Settings", "Servers"];

// The same cmdk styling the vendored CommandDialog uses.
const COMMAND_CLASS =
  "[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-muted-foreground [&_[cmdk-group]:not([hidden])_~[cmdk-group]]:pt-0 [&_[cmdk-group]]:px-2 [&_[cmdk-input-wrapper]_svg]:h-5 [&_[cmdk-input-wrapper]_svg]:w-5 [&_[cmdk-input]]:h-12 [&_[cmdk-item]]:px-2 [&_[cmdk-item]]:py-3 [&_[cmdk-item]_svg]:h-5 [&_[cmdk-item]_svg]:w-5";

// itemValue is what cmdk filters on and tells items apart by: the group,
// the label and the hint ("Settings: General (Organization)").
function itemValue(entry: PaletteNavEntry, prefix = ""): string {
  return `${prefix}${entry.group}: ${entry.label}${entry.hint ? ` (${entry.hint})` : ""}`;
}

// CommandPalette is ⌘K (spec §2): jump to an app on this server, a page, a
// Settings section or another server; New app, Add server, the theme; and
// whatever the open page registered (an app's Restart, …). An empty query
// starts with the recent destinations.
export function CommandPalette({ onAddServer }: { onAddServer?: () => void }) {
  const { open, setOpen, groups } = useCommandPalette();
  const navigate = useNavigate();
  const { servers, activeServerId } = useServers();
  const { apps } = useApps();
  const settings = useSettingsSections();
  const [, setTheme] = useThemePreference();
  const [query, setQuery] = useState("");

  const entries = useMemo(
    () => paletteNavEntries({ serverId: activeServerId, apps, servers, settings, standalone: isStandalone }),
    [activeServerId, apps, servers, settings],
  );
  // Read again on every open: another tab may have added to the list.
  const recent = useMemo(() => (open ? visibleRecent(readRecent(), entries) : []), [open, entries]);

  const close = () => {
    setOpen(false);
    setQuery("");
  };
  const go = (entry: PaletteNavEntry) => {
    pushRecent({ id: entry.id, label: entry.label, to: entry.to });
    close();
    navigate(entry.to);
  };
  const run = (action: () => void) => {
    close();
    action();
  };
  const navItem = (entry: PaletteNavEntry, prefix = "") => (
    <CommandItem key={prefix + entry.id} value={itemValue(entry, prefix)} keywords={entry.keywords} onSelect={() => go(entry)}>
      <span className="truncate">{entry.label}</span>
      {entry.hint ? <span className="ml-auto truncate text-xs text-muted-foreground">{entry.hint}</span> : null}
    </CommandItem>
  );

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
      <DialogContent className="overflow-hidden p-0 sm:max-w-xl" aria-describedby={undefined}>
        <DialogTitle className="sr-only">Search</DialogTitle>
        <Command loop className={COMMAND_CLASS}>
          <CommandInput value={query} onValueChange={setQuery} placeholder="Search apps, pages and actions…" />
          <CommandList className="max-h-[min(60vh,420px)]">
            <CommandEmpty>Nothing matches.</CommandEmpty>
            {query === "" && recent.length > 0 ? (
              <CommandGroup heading="Recent">{recent.map((entry) => navItem(entry, "Recent "))}</CommandGroup>
            ) : null}
            {groups.map((group) =>
              group.actions.length > 0 ? (
                <CommandGroup key={group.heading} heading={group.heading}>
                  {group.actions.map((action) => (
                    <CommandItem
                      key={action.id}
                      value={`${group.heading}: ${action.label}`}
                      keywords={action.keywords}
                      disabled={action.disabled}
                      onSelect={() => run(action.run)}
                    >
                      {action.icon ? <action.icon /> : null}
                      <span>{action.label}</span>
                    </CommandItem>
                  ))}
                </CommandGroup>
              ) : null,
            )}
            {NAV_GROUPS.map((name) => {
              const list = entries.filter((e) => e.group === name);
              return list.length > 0 ? (
                <CommandGroup key={name} heading={name}>
                  {list.map((entry) => navItem(entry))}
                </CommandGroup>
              ) : null;
            })}
            <CommandGroup heading="Actions">
              {entries.filter((e) => e.group === "Actions").map((entry) => navItem(entry))}
              {onAddServer ? (
                <CommandItem value="Actions: Add server" keywords={["server", "connect"]} onSelect={() => run(onAddServer)}>
                  <Server />
                  <span>Add server</span>
                </CommandItem>
              ) : null}
              {THEME_OPTIONS.map((theme) => (
                <CommandItem
                  key={theme.value}
                  value={`Actions: Theme: ${theme.label}`}
                  keywords={["appearance", "dark mode", "light mode"]}
                  onSelect={() => run(() => setTheme(theme.value))}
                >
                  <theme.icon />
                  <span>Theme: {theme.label}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
