import { api } from "../api/client";
export async function enableNotifications(): Promise<string> {
  if (
    !("Notification" in window) ||
    !("serviceWorker" in navigator) ||
    !("PushManager" in window)
  )
    throw new Error(
      "当前浏览器不支持推送，请使用支持推送的 HTTPS 浏览器或已安装的 PWA。",
    );
  const permission = await Notification.requestPermission();
  if (permission !== "granted")
    throw new Error("通知权限未开启。可在浏览器网站设置中调整。");
  let timeout: ReturnType<typeof setTimeout> | undefined;
  const registration = await Promise.race([
    navigator.serviceWorker.ready,
    new Promise<never>((_, reject) => {
      timeout = setTimeout(
        () => reject(new Error("通知服务尚未就绪，请刷新后重试。")),
        10000,
      );
    }),
  ]).finally(() => clearTimeout(timeout));
  let sub = await registration.pushManager.getSubscription();
  if (!sub) {
    const { publicKey } = await api.pushPublicKey();
    const raw = atob(
      publicKey.replace(/-/g, "+").replace(/_/g, "/") +
        "=".repeat((4 - (publicKey.length % 4)) % 4),
    );
    const key = new Uint8Array(raw.length);
    for (let i = 0; i < raw.length; i++) key[i] = raw.charCodeAt(i);
    sub = await registration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: key,
    });
  }
  const json = sub.toJSON();
  if (!json.endpoint || !json.keys?.p256dh || !json.keys?.auth)
    throw new Error("浏览器返回了无效的推送订阅。");
  await api.pushSubscribe({
    endpoint: json.endpoint,
    p256dh: json.keys.p256dh,
    auth: json.keys.auth,
    userAgent: navigator.userAgent,
  });
  return "当前设备通知已启用";
}
