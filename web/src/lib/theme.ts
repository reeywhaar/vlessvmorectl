import { useCallback, useEffect, useState, useSyncExternalStore } from "react";

export type Theme = "dark" | "light";

const STORAGE_KEY = "vlessvmore.theme";

/**
 * A theme is a preference, not a credential, so localStorage is the right place for it —
 * unlike anything else this application holds.
 */
function readStored(): Theme | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    return v === "dark" || v === "light" ? v : null;
  } catch {
    // Storage can throw in a private window or with cookies blocked. Falling back to the
    // system preference is a better outcome than a blank page.
    return null;
  }
}

function systemTheme(): Theme {
  return window.matchMedia?.("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

/** Applied before React mounts too, from an inline script, so there is no flash. */
export function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
}

export function initialTheme(): Theme {
  return readStored() ?? systemTheme();
}

export function useTheme() {
  const [theme, setTheme] = useState<Theme>(initialTheme);

  useEffect(() => {
    applyTheme(theme);
    try {
      localStorage.setItem(STORAGE_KEY, theme);
    } catch {
      /* the choice just will not persist */
    }
  }, [theme]);

  const toggle = useCallback(() => {
    setTheme((t) => (t === "dark" ? "light" : "dark"));
  }, []);

  return { theme, toggle };
}

/**
 * The theme in force, for a component that needs to read it rather than own it.
 *
 * Deliberately not a second useTheme. That hook holds the state, and a page has exactly one of
 * them beside its toggle; calling it again deeper down would mint an independent copy that never
 * hears the toggle and sits on a stale value. This reads the attribute applyTheme sets, which is
 * the one place both the hook and the pre-mount inline script agree on.
 */
export function useAppliedTheme(): Theme {
  return useSyncExternalStore(subscribe, applied, () => "dark");
}

function subscribe(onChange: () => void): () => void {
  const observer = new MutationObserver(onChange);
  observer.observe(document.documentElement, { attributeFilter: ["data-theme"] });
  return () => observer.disconnect();
}

function applied(): Theme {
  return document.documentElement.dataset.theme === "light" ? "light" : "dark";
}
