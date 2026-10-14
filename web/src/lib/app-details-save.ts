import type { AppsContextValue } from "@/context/apps-context-base";
import { buildSavePayload, stateFromDetail } from "@/lib/app-editor-io";

// AppDetailsInput is what Settings → General edits.
export type AppDetailsInput = { name: string; icon: string; color: string; description: string };

export type AppDetailsDeps = Pick<AppsContextValue, "rename" | "getApp" | "getRevisions" | "getPublicKey" | "saveApp">;

// saveAppDetails saves Settings → General. A new name goes through the
// rename route: a commit that redeploys nothing. The icon, colour and
// description go through the app's own save (a new version, with the files
// and variables as stored; only the fields that changed are replaced). That
// save redeploys only a running app with nothing undeployed; for a stopped
// app, or one with an undeployed draft, it is kept as a draft, so a colour
// never starts an app or deploys someone's draft — it goes live with the
// app's next deploy.
export async function saveAppDetails(
  deps: AppDetailsDeps,
  appId: string,
  before: AppDetailsInput,
  after: AppDetailsInput,
  running: boolean,
): Promise<{ renamed: boolean; saved: "none" | "deployed" | "draft" }> {
  const name = after.name.trim();
  const renamed = name !== before.name.trim();
  if (renamed) await deps.rename(appId, name);

  const icon = after.icon !== before.icon;
  const color = after.color !== before.color;
  const description = after.description !== before.description;
  if (!icon && !color && !description) return { renamed, saved: "none" };

  const state = stateFromDetail(appId, await deps.getApp(appId));
  state.config = {
    ...state.config,
    ...(icon ? { icon: after.icon } : {}),
    ...(color ? { color: after.color } : {}),
    ...(description ? { description: after.description } : {}),
  };
  const revisions = await deps.getRevisions(appId);
  const hasDraft = revisions.deployed !== "" && revisions.current !== revisions.deployed;
  const draft = !running || hasDraft;
  const payload = await buildSavePayload(state, deps.getPublicKey, appId);
  await deps.saveApp({ ...payload, draft });
  return { renamed, saved: draft ? "draft" : "deployed" };
}
