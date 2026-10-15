import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { CopyField } from "@/components/copy-field";
import { FieldMessage } from "@/components/form-field";
import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import type { AppEditorState, ExposedPort } from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  // serverIp is the server's address (its server_ip capability): a tcp or
  // udp entry is reached there.
  serverIp?: string;
  // errors holds pin:<id> messages (editorErrors).
  errors?: FieldErrors;
};

// PortsPanel lists the app's expose entries: the host port the server gave
// each (allocated on the first save), where it listens (localhost: only the
// built-in proxy reaches an http entry; LAN: a tcp/udp entry listens on
// every interface, with its address to copy), and Pin port, which keeps a
// port of the user's choice.
export function PortsPanel({ state, onChange, serverIp, errors = {} }: Props) {
  const entries = state.expose ?? [];
  if (entries.length === 0) return null;
  const ports = state.config.ports ?? {};
  const setPin = (e: ExposedPort, pin: { host: number | ""; pinned: boolean }) =>
    onChange({ ...state, config: { ...state.config, ports: { ...ports, [e.id]: pin } } });
  return (
    <ul aria-label="Ports" className="divide-y rounded-md border">
      {entries.map((e) => {
        const pin = ports[e.id];
        const pinned = pin?.pinned ?? false;
        const host = pinned ? pin.host : e.host_port;
        const lan = e.protocol !== "http";
        const pinId = `pin-${e.id}`;
        const error = errors[`pin:${e.id}`];
        return (
          <li key={e.id} className="space-y-2 px-3 py-2 text-sm">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <code className="font-mono">
                {e.container}:{e.port}
              </code>
              <span className="text-xs uppercase text-muted-foreground">{e.protocol}</span>
              <span className="rounded-md border px-1.5 text-xs" title={lan ? "Listens on every interface" : "Only the built-in proxy reaches it"}>
                {lan ? "LAN" : "localhost"}
              </span>
              <span className="ml-auto text-muted-foreground">
                {e.host_port ? `port ${e.host_port}` : "port allocated on deploy"}
              </span>
              <label className="flex items-center gap-2 text-xs">
                <Switch
                  aria-label={`Pin port of ${e.id}`}
                  checked={pinned}
                  onCheckedChange={(on) => setPin(e, { host: host || "", pinned: on })}
                />
                Pin port
              </label>
            </div>
            {pinned ? (
              <Input
                id={pinId}
                type="number"
                inputMode="numeric"
                aria-label={`Pinned port of ${e.id}`}
                value={pin.host}
                {...invalidAttrs(pinId, error, "h-8 w-32")}
                onChange={(ev) => setPin(e, { host: ev.target.value === "" ? "" : Number(ev.target.value), pinned: true })}
              />
            ) : null}
            <FieldMessage id={pinId} message={error} />
            {lan && serverIp && e.host_port ? <CopyField label={`Address of ${e.id}`} value={`${serverIp}:${e.host_port}`} /> : null}
          </li>
        );
      })}
    </ul>
  );
}
