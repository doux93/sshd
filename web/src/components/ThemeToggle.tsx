import { setTheme, useTheme } from "../workspace/theme";
import { Icon } from "./Icon";
export function ThemeToggle() {
  const theme = useTheme();
  return (
    <button
      className="icon-button"
      aria-label={theme === "dark" ? "切换浅色主题" : "切换深色主题"}
      title={theme === "dark" ? "切换浅色主题" : "切换深色主题"}
      onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
    >
      <Icon name={theme === "dark" ? "sun" : "moon"} size={17} />
    </button>
  );
}
