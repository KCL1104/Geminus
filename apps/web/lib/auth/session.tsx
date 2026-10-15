"use client";
// Auth boundary (T-014). The real OIDC session arrives with T-008 and the
// caller's role with membership discovery (D-008 / CB-17). Until a provider
// mounts AuthProvider with a session, the app is signed out: there is no
// development or demo session, so nothing can render tenant data by default.
import { createContext, type ReactNode, useContext } from "react";
import type { Session } from "../api/client";

export type Role = "owner" | "editor" | "viewer";

export type Auth = {
  session: Session | null;
  /** The caller's role in session.organizationId; null while unknown. */
  role: Role | null;
};

const AuthContext = createContext<Auth>({ session: null, role: null });

export function AuthProvider({ value, children }: { value: Auth; children: ReactNode }) {
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): Auth {
  return useContext(AuthContext);
}

/**
 * Whether to offer write actions. An unknown role is read-only. This only
 * shapes the UI: the API authorizes every write and the UI renders its 403.
 */
export function canEdit(role: Role | null): boolean {
  return role === "owner" || role === "editor";
}
