import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router";

import "./index.css";
import { App } from "./App";
import { ApiProvider } from "./api/ApiProvider";
import { ApiDispatcher } from "./api/dispatcher";
import { ProxyTransport } from "./api/transport";
import { makeQueryClient } from "./queries/client";
import { ReloadWatch, ReloadWatchProvider } from "./queries/reloadWatch";
import { applyTheme, initialTheme } from "./lib/theme";

// Before the first paint, so a dark-mode user does not get a white flash.
applyTheme(initialTheme());

// Client init: everything stateful is constructed here and injected, rather than being
// reached for as a module singleton from wherever it happens to be needed.
const dispatcher = new ApiDispatcher(new ProxyTransport());
const queryClient = makeQueryClient();
const reloadWatch = new ReloadWatch();

const root = document.getElementById("root");
if (!root) throw new Error("#root is missing from index.html");

createRoot(root).render(
  <StrictMode>
    <ApiProvider dispatcher={dispatcher}>
      <QueryClientProvider client={queryClient}>
        <ReloadWatchProvider value={reloadWatch}>
          <BrowserRouter>
            <App />
          </BrowserRouter>
        </ReloadWatchProvider>
      </QueryClientProvider>
    </ApiProvider>
  </StrictMode>,
);
