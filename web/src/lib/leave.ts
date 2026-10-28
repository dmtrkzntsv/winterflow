// leaveTo sends the browser to url, leaving the app (an OAuth client's
// redirect, a provider's sign-in). One place, so tests can stub it.
export function leaveTo(url: string) {
  window.location.assign(url);
}
