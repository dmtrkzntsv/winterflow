// `||` (not `??`) so the embedded-SPA production build, which pins these to
// empty strings (.env.production), falls back to the serving origin.
const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || window.location.origin;
const appBaseUrl = import.meta.env.VITE_APP_BASE_URL || window.location.origin;
const isStandalone = import.meta.env.VITE_APP_MODE === "standalone";

export { apiBaseUrl, appBaseUrl, isStandalone };
