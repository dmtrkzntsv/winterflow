import { defineConfig } from "tailwindcss";
import tailwindAnimate from "tailwindcss-animate";

export default defineConfig({
  darkMode: ["class"],
  content: ["./index.html", "./src/**/*.{ts,tsx,js,jsx}"],
  theme: {
    extend: {},
  },
  plugins: [tailwindAnimate],
});
