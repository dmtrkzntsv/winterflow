/// <reference types="vitest/config" />
import { fileURLToPath, URL } from "node:url";

import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The API routes the dev server forwards to the Go API, so `pnpm dev` is
// same-origin like the embedded build (the session cookie and the
// X-Winterflow-Request header need no CORS). /oauth/authorize is not here:
// it is the SPA's consent page.
const API_PATHS = ["/api", "/auth", "/.well-known", "/oauth/register", "/oauth/token", "/mcp"];

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // WINTERFLOW_DEV_API (env or web/.env*): where `pnpm dev` finds the API.
  const target = loadEnv(mode, process.cwd(), "").WINTERFLOW_DEV_API || "http://localhost:8080";
  return {
    plugins: [react(), tailwindcss()],
    server: {
      proxy: Object.fromEntries(API_PATHS.map((p) => [p, { target, changeOrigin: false }])),
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
    // Vitest (pnpm test / test:coverage). Coverage feeds CI's diff gate and
    // ratchet through coverage/lcov.info; vendored shadcn/ui is excluded.
    test: {
      environment: "jsdom",
      setupFiles: ["./src/test/setup.ts"],
      // Form tests type whole dialogs (about 1 s idle); under coverage on a
      // loaded runner the 5 s default is too tight.
      testTimeout: 15_000,
      coverage: {
        provider: "v8",
        reporter: ["text", "lcov"],
        reportsDirectory: "./coverage",
        include: ["src/**"],
        exclude: ["src/components/ui/**", "src/main.tsx", "**/*.d.ts"],
      },
    },
  };
});
