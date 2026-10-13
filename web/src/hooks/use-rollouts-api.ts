import { useMemo } from "react";

import { createRolloutsApi, type RolloutsApi } from "@/lib/rollouts";

// useRolloutsApi is the rollouts client: injected (a test's), else one
// built for this component.
export function useRolloutsApi(injected?: RolloutsApi): RolloutsApi {
  return useMemo(() => injected ?? createRolloutsApi(), [injected]);
}
