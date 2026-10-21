// buildInstallCommand is the one-line agent installer for a machine that
// should join the Winterflow at apiOrigin (no trailing slash needed).
export function buildInstallCommand(apiOrigin: string): string {
  return `curl -fsSL https://get.winterflow.dev/agent | sudo bash -s -- --url ${apiOrigin.replace(/\/+$/, "")}`;
}
