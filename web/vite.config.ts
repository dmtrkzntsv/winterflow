import { fileURLToPath, URL } from "node:url";

import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    // Bind on all interfaces so other machines (e.g. over Tailscale) can reach
    // the dev server, not just localhost on this host.
    host: true,
    // Vite rejects requests whose Host header isn't allow-listed; permit access
    // via the Tailscale hostname.
    allowedHosts: true,
  },
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
});
