/// <reference lib="webworker" />
// Custom service worker for sshx.
//
// Uses vite-plugin-pwa'\''s injectManifest strategy: this file is the
// entry point the plugin runs through esbuild to produce dist/sw.js.
// We keep all precaching / NavigationRoute logic in the plugin config
// (see vite.config.ts); this file only adds the Web Push event
// handlers because those need a real listener, not just a route.

// The plugin injects a self.__WB_MANIFEST we precache at install time.
import { precacheAndRoute } from "workbox-precaching";
import { NavigationRoute, registerRoute } from "workbox-routing";
import { createHandlerBoundToURL } from "workbox-precaching";

declare const self: ServiceWorkerGlobalScope;

self.skipWaiting();
(self as any).clients?.claim?.();

// 1. Precache the SPA shell + hashed assets.
precacheAndRoute((self as any).__WB_MANIFEST || []);

// 2. SPA navigation fallback. The plugin also configures
//    navigateFallbackDenylist for /api and /ws, so this only kicks
//    in for actual SPA routes.
const handler = createHandlerBoundToURL("/index.html");
const navRoute = new NavigationRoute(handler, {
  denylist: [/^\/api\//, /^\/ws($|\?)/],
});
registerRoute(navRoute);

// 3. Web Push handler. The server pushes a JSON payload; we turn it
//    into a system notification and remember it in `event.waitUntil`
//    so the notification survives the worker being torn down.
self.addEventListener("push", (event) => {
  let data: any = {};
  if (event.data) {
    try {
      data = event.data.json();
    } catch {
      // non-JSON payload: just put the raw text into the body.
      data = { body: event.data.text() };
    }
  }
  const title = data.title || "sshx alert";
  const opts: NotificationOptions = {
    body: data.body || "",
    icon: data.icon || "/icons/icon-192.png",
    badge: data.badge || "/icons/icon-192.png",
    tag: data.tag,
    data: { ...(data.data || {}), url: data.url },
    requireInteraction: data.severity === "critical",
  };
  event.waitUntil(self.registration.showNotification(title, opts));
});

// 4. Notification click → focus or open the URL the alert was about.
//    We send the URL via event.notification.data.url (set above) so
//    the server can change routing without rebuilding the SW.
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  let target = new URL("/?panel=alerts", self.location.origin);
  try {
    const requested = new URL(
      event.notification.data?.url || "/?panel=alerts",
      self.location.origin,
    );
    if (requested.origin === self.location.origin) target = requested;
  } catch {
    /* invalid payload uses the local alert history */
  }
  const url = target.href;
  event.waitUntil(
    (async () => {
      const allClients = await self.clients.matchAll({
        type: "window",
        includeUncontrolled: true,
      });
      for (const c of allClients) {
        // Match on the same origin so we focus the right tab.
        if (new URL(c.url).origin === self.location.origin) {
          await c.navigate(url);
          await c.focus();
          return;
        }
      }
      await self.clients.openWindow(url);
    })(),
  );
});

// 5. Optional: subscribe() is a client-side call, but we accept a
//    postMessage from the page that triggers it. We could implement
//    pushSubscriptionChange handling here too.
