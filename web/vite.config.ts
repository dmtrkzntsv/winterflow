/// <reference types="vitest/config" />
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
  // Vitest (pnpm test / test:coverage). Coverage feeds CI's diff gate and
  // ratchet through coverage/lcov.info; vendored shadcn/ui is excluded.
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    coverage: {
      provider: "v8",
      reporter: ["text", "lcov"],
      reportsDirectory: "./coverage",
      include: ["src/**"],
      exclude: ["src/components/ui/**", "src/main.tsx", "**/*.d.ts"],
    },
  },
});
