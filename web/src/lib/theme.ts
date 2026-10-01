export type Theme = "light" | "dark" | "system";

const KEY = "memory-theme";

export function getTheme(): Theme {
  try {
    return (localStorage.getItem(KEY) as Theme) || "system";
  } catch {
    return "system";
  }
}

export function applyTheme(theme: Theme) {
  try {
    localStorage.setItem(KEY, theme);
  } catch {
    /* storage unavailable */
  }
  const dark = theme === "dark" || (theme === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
}
