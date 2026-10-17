import { defaultFilter } from "cmdk";
import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Server } from "lucide-react";

import { StatusDot } from "@/components/status";
import { CatalogLogo } from "@/components/catalog-logo";

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
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { useTemplateLooks } from "@/hooks/use-template-looks";
import { useThemePreference } from "@/hooks/use-theme";
import {
  paletteNavEntries,
  pushRecent,
  readRecent,
  visibleRecent,
  type PaletteGroup,
  type PaletteNavEntry,
} from "@/lib/palette";
import { appStatus } from "@/lib/status";
import { THEME_OPTIONS } from "@/lib/theme-options";

const NAV_GROUPS: PaletteGroup[] = ["Apps", "Pages", "Templates", "Settings", "Servers"];

// The same cmdk styling the vendored CommandDialog uses.
const COMMAND_CLASS =
  "[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-muted-foreground [&_[cmdk-group]:not([hidden])_~[cmdk-group]]:pt-0 [&_[cmdk-group]]:px-2 [&_[cmdk-input-wrapper]_svg]:h-5 [&_[cmdk-input-wrapper]_svg]:w-5 [&_[cmdk-input]]:h-12 [&_[cmdk-item]]:px-2 [&_[cmdk-item]]:py-2.5 [&_[cmdk-item]_svg]:h-4 [&_[cmdk-item]_svg]:w-4";

// An item's cmdk value is its searchable text, then ID_MARK and its id. The id
// keeps two same-named apps or servers apart (cmdk treats equal values as one
// item, so the second could not be reached with the keyboard) but is never
// searched: an app's uuid or a settings path would match any short query.
const ID_MARK = "\u001f";

function withId(text: string, id: string): string {
  return `${text}${ID_MARK}${id}`;
}

// paletteFilter scores the text before the id (group, label, hint) fuzzily and
// each keyword only where the query is a part of it: fuzzy matching across a
// pile of keywords lets short queries hit unrelated items.
function paletteFilter(value: string, search: string, keywords?: string[]): number {
  const at = value.indexOf(ID_MARK);
  const text = defaultFilter(at < 0 ? value : value.slice(0, at), search);
  const query = search.toLowerCase();
  const viaKeyword = (keywords ?? [])
    .filter((keyword) => keyword.toLowerCase().includes(query))
    .map((keyword) => defaultFilter(keyword, search) / 2);
  return Math.max(text, ...viaKeyword);
}

// Enter is the "↵" on the highlighted item: what Enter does.
function Enter() {
  return (
    <kbd aria-hidden className="hidden font-sans text-xs text-muted-foreground in-data-[selected=true]:inline">
      ↵
    </kbd>
  );
}

// itemValue is the group, the label and the hint ("Settings: General (Organization)").
function itemValue(entry: PaletteNavEntry, prefix = ""): string {
  return withId(`${prefix}${entry.group}: ${entry.label}${entry.hint ? ` (${entry.hint})` : ""}`, entry.id);
}

// CommandPalette is ⌘K (spec §2): jump to an app on this server, a page, a
// Settings section or another server; New app, Add server, the theme; and
// whatever the open page registered (an app's Restart, …). An empty query
// starts with the recent destinations.
export function CommandPalette({ onAddServer }: { onAddServer?: () => void }) {
  const { open, setOpen, groups } = useCommandPalette();
  const navigate = useNavigate();
  const { servers, activeServerId, selectServer } = useServers();
  const { apps, statusByApp, deployFailures } = useApps();
  const settings = useSettingsSections();
  const templatesApi = useTemplatesApi();
  const [templates, setTemplates] = useState<Awaited<ReturnType<typeof templatesApi.listTemplates>>>([]);
  const looks = useTemplateLooks(templatesApi, templates);
  useEffect(() => {
    if (!open) return;
    let current = true;
    templatesApi.listTemplates().then((list) => { if (current) setTemplates(list); }, () => {});
    return () => { current = false; };
  }, [open, templatesApi]);
  const [, setTheme] = useThemePreference();
  const [query, setQuery] = useState("");

  const entries = useMemo(
    () => paletteNavEntries({ serverId: activeServerId, apps, servers, settings, standalone: isStandalone, templates, templateSummaries: Object.fromEntries(Object.entries(looks).map(([id, look]) => [id, look.summary])) }),
    [activeServerId, apps, servers, settings, templates, looks],
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
    // Switching server keeps the page kind, as the switcher and ⌘1–⌘9 do.
    if (entry.serverId) selectServer(entry.serverId);
    else navigate(entry.to);
  };
  const run = (action: () => void) => {
    close();
    action();
  };
  // A destination leads with its icon, an app with its status dot; the
  // hint and the ↵ sit at the end.
  const navItem = (entry: PaletteNavEntry, prefix = "") => (
    <CommandItem key={prefix + entry.id} value={itemValue(entry, prefix)} keywords={entry.keywords} onSelect={() => go(entry)}>
      {entry.templateId && templates.find((t) => t.id === entry.templateId && t.source === "catalog") ? (
        <CatalogLogo template={templates.find((t) => t.id === entry.templateId)!} className="size-5 shrink-0 rounded text-xs" />
      ) : entry.appId ? (
        <span className="flex size-4 shrink-0 items-center justify-center" data-testid="palette-app-status">
          <StatusDot tone={appStatus(statusByApp[entry.appId], Boolean(deployFailures?.[entry.appId])).tone} />
        </span>
      ) : entry.icon ? (
        <entry.icon className="text-muted-foreground" />
      ) : null}
      <span className="truncate">{entry.label}</span>
      <span className="ml-auto flex shrink-0 items-center gap-2">
        {entry.hint ? <span className="truncate text-xs text-muted-foreground">{entry.hint}</span> : null}
        <Enter />
      </span>
    </CommandItem>
  );

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
      <DialogContent className="overflow-hidden p-0 sm:max-w-xl" aria-describedby={undefined}>
        <DialogTitle className="sr-only">Search</DialogTitle>
        <Command loop filter={paletteFilter} className={COMMAND_CLASS}>
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
                      value={withId(`${group.heading}: ${action.label}`, action.id)}
                      keywords={action.keywords}
                      disabled={action.disabled}
                      onSelect={() => run(action.run)}
                    >
                      {action.icon ? <action.icon className="text-muted-foreground" /> : null}
                      <span>{action.label}</span>
                      <span className="ml-auto">
                        <Enter />
                      </span>
                    </CommandItem>
                  ))}
                </CommandGroup>
              ) : null,
            )}
            {NAV_GROUPS.map((name) => {
              const list = entries.filter((e) => e.group === name && (name !== "Templates" || query !== "" || e.templateId === undefined || templates.find((t) => t.id === e.templateId)?.source === "org"));
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
                  <Server className="text-muted-foreground" />
                  <span>Add server</span>
                  <span className="ml-auto">
                    <Enter />
                  </span>
                </CommandItem>
              ) : null}
              {THEME_OPTIONS.map((theme) => (
                <CommandItem
                  key={theme.value}
                  value={`Actions: Theme: ${theme.label}`}
                  keywords={["appearance", "dark mode", "light mode"]}
                  onSelect={() => run(() => setTheme(theme.value))}
                >
                  <theme.icon className="text-muted-foreground" />
                  <span>Theme: {theme.label}</span>
                  <span className="ml-auto">
                    <Enter />
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
