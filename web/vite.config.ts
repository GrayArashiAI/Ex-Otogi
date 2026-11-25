import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { compression } from "vite-plugin-compression2";

// In development, `pnpm dev` serves the panel and passes /api to a bot
// running locally (OTOGI_API, default the management address). A build
// also writes each file over 1 KiB compressed beside it, as .br and .gz,
// which the bot sends to a browser that accepts them.
export default defineConfig({
  plugins: [
    tanstackRouter({ target: "react", autoCodeSplitting: true }),
    react(),
    tailwindcss(),
    compression({ algorithms: ["brotliCompress", "gzip"], threshold: 1024 }),
  ],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  server: { proxy: { "/api": process.env.OTOGI_API ?? "http://127.0.0.1:8090" } },
  // dist/ keeps its .gitkeep for go:embed; `make web` clears the old build.
  build: { emptyOutDir: false },
});
