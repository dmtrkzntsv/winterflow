import { useEffect } from "react";
import { useNavigate } from "react-router-dom";

import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { announceCreate, leftBehind } from "@/hooks/use-create-app";
import { onCreateExpiry, onUnattendedCreate } from "@/lib/held-creates";

// CreateOutcomes tells, in a toast, how a create ended when no create form
// is open for its server (the user left it while the agent worked): ready,
// with Open; couldn't start, the failure noted on the app it left (found
// in the agent's list, read again). At a held create's cap it reads that
// list once before the name is freed, so an app that did land is listed.
export function CreateOutcomes() {
  const { reconcile, noteDeployFailure } = useApps();
  const { activeServerId } = useServers();
  const navigate = useNavigate();

  useEffect(
    () =>
      onUnattendedCreate((create, outcome) => {
        // Another server's list can't be read from here.
        const here = create.serverId === activeServerId;
        if (!outcome.failed || outcome.left !== undefined || !here) {
          announceCreate(create, outcome, navigate);
          return;
        }
        void leftBehind(reconcile, create).then((left) => {
          if (left) noteDeployFailure(left.id, outcome.error);
          announceCreate(create, { ...outcome, left }, navigate);
        });
      }),
    [activeServerId, reconcile, noteDeployFailure, navigate],
  );

  useEffect(
    () => onCreateExpiry((create) => (create.serverId === activeServerId ? reconcile() : Promise.resolve())),
    [activeServerId, reconcile],
  );

  return null;
}
