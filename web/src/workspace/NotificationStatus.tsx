import { useEffect, useState } from "react";
export function NotificationStatus() {
  const [status, setStatus] = useState("");
  useEffect(() => {
    const update = () => {
      if (!window.isSecureContext) {
        setStatus("手机通知需要 HTTPS 地址，请通过安全地址打开工作台。");
        return;
      }
      if (!("Notification" in window) || !("PushManager" in window)) {
        setStatus(
          "当前浏览器不支持推送。iPhone/iPad 可将工作台添加到主屏幕后再打开。",
        );
        return;
      }
      if (Notification.permission === "denied") {
        setStatus("通知已被浏览器拒绝，请在网站设置中允许后重试。");
        return;
      }
      setStatus(
        Notification.permission === "granted"
          ? "通知权限已允许；点击启用，完成当前设备的推送订阅。"
          : "点击下方按钮开启通知权限；告警历史始终可以在此查看。",
      );
    };
    update();
    window.addEventListener("focus", update);
    return () => window.removeEventListener("focus", update);
  }, []);
  return <p className="mb-2 text-xs leading-5 text-slate-400">{status}</p>;
}
