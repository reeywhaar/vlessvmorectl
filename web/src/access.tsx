import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "./index.css";
import { AccessPage } from "./access/AccessPage";
import { tokenFromPath } from "./access/token";
import { applyTheme, initialTheme } from "./lib/theme";

/**
 * The subscriber island's entry point.
 *
 * Deliberately thin, and deliberately importing almost nothing. This is the boundary
 * vite.config.ts describes: whatever is reachable from this file is what gets served to
 * somebody holding a share link, so every import here is a decision rather than a habit.
 *
 * Three things the panel's main.tsx has that this does not, each left out on purpose:
 *
 *   - **No router.** There is exactly one URL shape, /access/:token, and reading it out
 *     of location.pathname is four lines. Pulling in react-router to do that would put a
 *     history stack and a matcher into a page with nowhere to navigate to.
 *   - **No QueryClient.** The page makes one request and does not poll — see AccessPage
 *     for why polling would be actively wrong here — so a cache, a retry policy and a
 *     background-refetch scheduler are machinery with nothing to manage.
 *   - **No ApiDispatcher or ProxyTransport.** Those exist to attach a session and to
 *     route node calls through /api/proxy, and this page has no session and must never
 *     touch that endpoint.
 */

// Before the first paint, so a dark-mode user does not get a white flash.
applyTheme(initialTheme());

const root = document.getElementById("root");
if (!root) throw new Error("#root is missing from access.html");

createRoot(root).render(
  <StrictMode>
    <AccessPage token={tokenFromPath(window.location.pathname)} />
  </StrictMode>,
);
