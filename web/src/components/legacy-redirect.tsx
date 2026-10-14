import { Navigate, useLocation, useParams, type Params } from "react-router-dom";

import { withQuery } from "@/lib/routes";

export type RedirectTarget =
  | string
  | ((at: { params: Readonly<Params<string>>; search: URLSearchParams }) => string);

// withoutTab is a query string without its ?tab=: on an old URL it only
// chose the target, and the new page would read it as one of its own tabs.
function withoutTab(search: string): string {
  const params = new URLSearchParams(search);
  params.delete("tab");
  const rest = params.toString();
  return rest ? `?${rest}` : "";
}

// LegacyRedirect sends a pre-rebuild URL (a bookmark, an alert e-mail, a
// link in an open tab) to its new place in one hop, replacing the history
// entry. The hash and the query (minus ?tab=) carry over.
export function LegacyRedirect({ to }: { to: RedirectTarget }) {
  const params = useParams();
  const location = useLocation();
  const target = typeof to === "string" ? to : to({ params, search: new URLSearchParams(location.search) });
  return (
    <Navigate
      replace
      to={withQuery(target, withoutTab(location.search), location.hash)}
      state={location.state}
    />
  );
}
