import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "./index.css";
import { AccessPage } from "./access/AccessPage";
import { tokenFromPath } from "./access/token";
import { applyTheme, initialTheme } from "./lib/theme";

/**
 * The subscriber island's entry point.
 *
 * Whatever is reachable from this file is what a share-link holder is served, so every
 * import is a decision. Omitted on purpose: the router (one URL shape), the QueryClient
 * (one request, no polling), and the dispatcher (no session, and it must never reach
 * /api/proxy).
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
