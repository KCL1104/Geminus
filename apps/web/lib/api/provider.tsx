"use client";
import { createContext, type ReactNode, useContext, useMemo } from "react";
import { useAuth } from "../auth/session";
import { apiBaseUrl } from "../config";
import { type ApiClient, createApiClient } from "./client";

const ApiContext = createContext<ApiClient | null>(null);

/** Tests inject a client; the app builds one from config and the auth boundary. */
export function ApiProvider({ client, children }: { client: ApiClient; children: ReactNode }) {
  return <ApiContext.Provider value={client}>{children}</ApiContext.Provider>;
}

export function useApi(): ApiClient {
  const injected = useContext(ApiContext);
  const { session } = useAuth();
  // A new session (or sign-out) yields a new client, so every page reloads with it.
  const built = useMemo(() => createApiClient({ baseUrl: apiBaseUrl(), session: () => session }), [session]);
  return injected ?? built;
}
