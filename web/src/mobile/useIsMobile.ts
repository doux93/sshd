import { useEffect, useState } from "react";

// Width-based mobile detection. We deliberately do NOT sniff pointer:coarse
// (DESIGN 4.1): touch laptops / all-in-ones report coarse pointers at full
// width and would be misclassified as phones. A narrow viewport is a safe
// proxy; the mobile layout is what flips the terminal to passive read-only,
// so a misfire here would at worst shrink the desktop chrome.
export function useIsMobile(): boolean {
  const [mobile, setMobile] = useState(
    typeof window !== "undefined" && window.matchMedia("(max-width: 768px)").matches,
  );
  useEffect(() => {
    const mq = window.matchMedia("(max-width: 768px)");
    const handler = (e: MediaQueryListEvent) => setMobile(e.matches);
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, []);
  return mobile;
}
