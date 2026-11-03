import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { defineConfig, loadEnv } from "vite";
import { apiProxyTarget } from "./dev-api-target";

export default defineConfig(({ mode, command }) => {
  const environment = loadEnv(mode, import.meta.dirname, "");
  const proxyTarget = apiProxyTarget(
    environment.MARKET_SHARE_FRONTEND_BACKEND_URL ||
      environment.VITE_DEV_API_TARGET
  );
  return {
    // A .env VITE_API_URL must not bypass the development proxy to production.
    define:
      command === "serve"
        ? { "import.meta.env.VITE_API_URL": JSON.stringify("") }
        : undefined,
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        "@": path.resolve(import.meta.dirname, "client", "src"),
      },
    },
    root: path.resolve(import.meta.dirname, "client"),
    server: {
      proxy: {
        "/api": {
          target: proxyTarget,
          changeOrigin: false,
          ws: true,
        },
      },
    },
    build: {
      outDir: path.resolve(import.meta.dirname, "dist", "public"),
      emptyOutDir: true,
      rollupOptions: {
        output: {
          manualChunks(id) {
            const normalizedId = id.split(path.sep).join("/");
            if (!normalizedId.includes("/node_modules/")) return;
            if (normalizedId.includes("/node_modules/lucide-react/"))
              return "icons";
          },
        },
      },
    },
  };
});
