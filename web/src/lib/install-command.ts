// buildInstallCommand is the one-line agent installer for a machine that
// should join the Winterflow at apiOrigin (no trailing slash needed).
export function buildInstallCommand(apiOrigin: string): string {
  return `curl -fsSL https://get.winterflow.dev/agent | sudo bash -s -- --url ${apiOrigin.replace(/\/+$/, "")}`;
}

// buildReenrollCommand enrolls an already enrolled machine again (after its
// server was removed): the installer sets the old certificate aside.
export function buildReenrollCommand(apiOrigin: string): string {
  return `${buildInstallCommand(apiOrigin)} --reenroll`;
}
