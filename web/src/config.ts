const apiBaseUrl =
  import.meta.env.VITE_API_BASE_URL ?? window.location.origin
const appBaseUrl =
  import.meta.env.VITE_APP_BASE_URL ?? window.location.origin

export { apiBaseUrl, appBaseUrl }
