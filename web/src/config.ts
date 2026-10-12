// `||` (not `??`) so the embedded-SPA production build, which pins these to
// empty strings (.env.production), falls back to the serving origin.
const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || window.location.origin;
const appBaseUrl = import.meta.env.VITE_APP_BASE_URL || window.location.origin;
// apiBase is apiBaseUrl without a trailing slash, for `${apiBase}/api/...`.
const apiBase = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;
const isStandalone = import.meta.env.VITE_APP_MODE === "standalone";

export { apiBase, apiBaseUrl, appBaseUrl, isStandalone };
