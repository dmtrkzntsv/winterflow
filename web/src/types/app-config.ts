// AppConfig mirrors the deployable app definition (v1 parity, trimmed to what
// v2 supports: no extensions). It is stored by the agent as config.json and
// carries the metadata for files and variables; the actual contents travel
// alongside in the create/save payload.

export type AppFileMeta = {
  id: string;
  filename: string;
  is_encrypted: boolean;
  // storedAs: the name the agent holds this secret file under (it was read
  // back as the "<encrypted>" placeholder). Editor-only, never sent. A
  // renamed file no longer stands for it (the agent keys secrets by name).
  storedAs?: string;
};

export type AppVariableMeta = {
  id: string;
  name: string;
  is_encrypted: boolean;
  // storedAs: the name the agent holds a secret value for this variable
  // under (it was read back as the "<encrypted>" placeholder). Editor-only,
  // never sent. A renamed variable no longer stands for it (the agent keys
  // secrets by name), so it needs a new value.
  storedAs?: string;
};

export type AppSourceConfig = {
  repo_url: string;
  branch: string;
  compose_path?: string;
  auto_update: boolean;
  poll_seconds?: number;
  // Whether an access token is stored on the agent (the token itself never
  // travels back; edits send the "<encrypted>" placeholder to keep it).
  token_set?: boolean;
};

export type IngressDomain = {
  id: string; // localId, editor-only — stripped on save
  domain: string;
  // expose names an http expose entry (<service>:<port>) the hostname
  // proxies to; without it, upstream_port is a port the compose file
  // publishes itself.
  expose?: string;
  upstream_port: number | ""; // "" while the field is empty in the editor
  ssl: boolean;
};

// ExposedPort is an x-winterflow expose entry with the host port the agent
// allocated (app.get's expose; host_port 0 until the first save).
export type ExposedPort = {
  id: string; // <container>:<port>/<protocol>
  container: string;
  port: number;
  protocol: "http" | "tcp" | "udp";
  host_port: number;
  bind: string;
  pinned: boolean;
};

// PortPin is an entry of the config's ports: the host port of an expose
// entry, pinned when the user chose it.
export type PortPin = { host: number | ""; pinned: boolean };

// exposeTarget is how an ingress hostname names an expose entry.
export function exposeTarget(e: ExposedPort): string {
  return `${e.container}:${e.port}`;
}

export type IngressRedirect = {
  id: string;
  domain: string;
  path: string; // "" = whole-domain redirect
  to: string;
  code: 301 | 302 | 307 | 308;
  ssl: boolean; // only meaningful when path === ""
};

export type AppIngress = {
  domains: IngressDomain[];
  redirects: IngressRedirect[];
};

export type AppConfig = {
  id: string;
  name: string;
  description?: string;
  icon?: string;
  color?: string;
  version?: string;
  files: AppFileMeta[];
  variables: AppVariableMeta[];
  source?: AppSourceConfig;
  ingress?: AppIngress;
  // ports are the stored host ports by expose entry id; only the pinned
  // ones are sent (a save without them unpins).
  ports?: Record<string, PortPin>;
};

// StorageVariable is a WF_STORAGE_ variable the app's compose files
// reference (app.get's storage_variables): a name and its storage, never a
// value.
export type StorageVariable = {
  name: string;
  storage: string;
  secret: boolean;
};

// DeclaredVariable is a variable the compose files declare under
// x-winterflow (app.get's declared_variables): the editor shows these first,
// as a form. default is the compose file's own value (never a secret's).
export type DeclaredVariable = {
  name: string;
  required: boolean;
  encrypted: boolean;
  pattern?: string;
  description?: string;
  default: string | null;
  services: string[] | null;
};

// Editor working state: the config metadata plus content maps keyed by id.
export type AppEditorState = {
  config: AppConfig;
  files: Record<string, string>; // file id -> content
  variables: Record<string, string>; // variable id -> value
  // New/replacement repo token typed this session ("" = keep/absent).
  sourceToken?: string;
  // Read-only rows from the last app.get: provided by the server's
  // storages, never saved with the app's variables.
  storageVariables: StorageVariable[];
  // The variables the compose files declare (last app.get; none for a new
  // app): their values are ordinary variables, edited through a form.
  declared?: DeclaredVariable[];
  // The expose entries with their host ports (last app.get).
  expose?: ExposedPort[];
};

let counter = 0;
// localId produces a stable-enough client id for new files/variables without
// pulling in a uuid dependency (the agent keys on name, not this id).
export function localId(prefix: string): string {
  counter += 1;
  return `${prefix}-${counter}-${Math.floor(performance.now())}`;
}

// COMPOSE_FILENAMES are the compose file names, in compose's load order
// (model.ComposeFileNames).
export const COMPOSE_FILENAMES: readonly string[] = [
  "compose.yaml",
  "compose.yml",
  "docker-compose.yml",
  "docker-compose.yaml",
];

// isComposeFile says whether a file name is one compose finds by itself.
export function isComposeFile(name: string): boolean {
  return COMPOSE_FILENAMES.includes(name.trim());
}

// defaultCompose is the example compose file a new app or template starts
// from, publishing hostPort (a number, or "\${PORT}" in a template).
export function defaultCompose(hostPort: number | string = 8080): string {
  return `services:
  app:
    image: nginx:alpine
    ports:
      # host:container — change the host port if it's already in use
      - "${hostPort}:80"
`;
}

export const DEFAULT_COMPOSE = defaultCompose();
