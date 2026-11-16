import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { initAuth } from "./api/client";
import { wsClient } from "./ws/protocol";
import { LoginPage } from "./pages/Login";
import "./styles.css";
import { initTheme } from "./workspace/theme";
initTheme();

const root = createRoot(document.getElementById("root")!);
async function start() {
  root.render(
    <div className="flex h-screen items-center justify-center bg-slate-900 text-slate-300">
      正在连接工作台…
    </div>,
  );
  try {
    await initAuth();
    const login = window.location.pathname === "/login";
    if (!login) wsClient.connect();
    root.render(<StrictMode>{login ? <LoginPage /> : <App />}</StrictMode>);
  } catch (error) {
    root.render(
      <div className="flex h-screen flex-col items-center justify-center gap-4 bg-slate-900 text-slate-200">
        <p>{error instanceof Error ? error.message : "连接失败"}</p>
        <button
          className="rounded bg-blue-600 px-4 py-2"
          onClick={() => void start()}
        >
          重试
        </button>
      </div>,
    );
  }
}
void start();
