import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { VitePWA } from "vite-plugin-pwa";

// Dev: `npm run dev` proxies API/WS to the Go server (default 127.0.0.1:8722).
// Prod: `npm run build` emits web/dist, embedded into the Go binary.
//
// PWA: precaches the SPA shell so the "Add to Home Screen" flow boots
// offline; runtime caching is deliberately narrow — SSH sessions and
// monitor data live on the server, so the phone only ever needs the
// shell + the auth/status endpoint.
export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    VitePWA({
      registerType: "autoUpdate",
      injectRegister: "auto", // emits /registerSW.js + <link rel="manifest">
      includeAssets: [
        "favicon.svg",
        "icons/icon-192.png",
        "icons/icon-512.png",
        "icons/icon-maskable-512.png",
        "icons/apple-touch-icon.png",
      ],
      manifest: {
        name: "SSH Workbench",
        short_name: "sshx",
        description:
          "\u5e38\u5f00\u7684 SSH \u63a7\u5236\u53f0\uff1a\u7ec8\u7aef\u3001SFTP\u3001\u76d1\u63a7\u4e0e\u544a\u8b66\uff0c\u624b\u673a\u4e5f\u80fd\u7528\u3002",
        lang: "zh-CN",
        start_url: "/?utm_source=pwa",
        scope: "/",
        display: "standalone",
        orientation: "any",
        background_color: "#0f172a", // slate-900, matches tailwind base
        theme_color: "#0f172a",
        categories: ["developer-tools", "productivity", "utilities"],
        icons: [
          { src: "/icons/icon-192.png", sizes: "192x192", type: "image/png" },
          { src: "/icons/icon-512.png", sizes: "512x512", type: "image/png" },
          {
            src: "/icons/icon-maskable-512.png",
            sizes: "512x512",
            type: "image/png",
            purpose: "maskable",
          },
        ],
      },
      strategies: "injectManifest",
      srcDir: "src",
      filename: "sw.ts",
      injectManifest: {
        // The plugin wraps the SW with a small preamble that defines
        // self.__WB_MANIFEST. We keep our SW thin so this stays safe.
        injectionPoint: "self.__WB_MANIFEST",
        // Precaches the static SPA shell + hashed assets. The runtime
        // API/WS paths are excluded (the SW treats them as network-only).
        globPatterns: ["**/*.{js,css,html,svg,png,ico,webmanifest}"],
        globIgnores: ["sw.js", "workbox-*.js", "registerSW.js"],
      },
      devOptions: {
        enabled: false,
      },
    }),
  ],
  server: {
    proxy: {
      "/api": { target: "http://127.0.0.1:8722", changeOrigin: false },
      "/ws": { target: "ws://127.0.0.1:8722", ws: true },
    },
  },
  build: { chunkSizeWarningLimit: 1024 },
});
