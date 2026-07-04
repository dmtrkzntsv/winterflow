// AppConfig mirrors the deployable app definition (v1 parity, trimmed to what
// v2 supports: no extensions). It is stored by the agent as config.json and
// carries the metadata for files and variables; the actual contents travel
// alongside in the create/save payload.

export type AppFileMeta = {
  id: string;
  filename: string;
  is_encrypted: boolean;
};

export type AppVariableMeta = {
  id: string;
  name: string;
  is_encrypted: boolean;
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
};

// Editor working state: the config metadata plus content maps keyed by id.
export type AppEditorState = {
  config: AppConfig;
  files: Record<string, string>; // file id -> content
  variables: Record<string, string>; // variable id -> value
  // New/replacement repo token typed this session ("" = keep/absent).
  sourceToken?: string;
};

let counter = 0;
// localId produces a stable-enough client id for new files/variables without
// pulling in a uuid dependency (the agent keys on name, not this id).
export function localId(prefix: string): string {
  counter += 1;
  return `${prefix}-${counter}-${Math.floor(performance.now())}`;
}

export const DEFAULT_COMPOSE = `services:
  app:
    image: nginx:alpine
    ports:
      # host:container — change the host port if it's already in use
      - "8088:80"
`;
