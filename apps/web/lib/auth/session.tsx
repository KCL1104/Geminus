"use client";
// Auth boundary (T-014). The root layout mounts the signed-in bootstrap
// (app/_components/auth-bootstrap.tsx, T-043 follow-up): with a BFF session
// it loads GET /v1/me (D-045) and provides the chosen organization as the
// session, its role, and the account (the bootstrap's state and actions).
// Without a provider, or without a session, the app is signed out: there is
// no development or demo session, so nothing can render tenant data by
// default. Tests and the editor's review preview mount a fixed session.
import { createContext, type ReactNode, useContext } from "react";
import type { Session } from "../api/client";
import type { Problem } from "../api/problem";
import type { Bootstrap } from "./bootstrap";

export type Role = "owner" | "editor" | "viewer";

/** The signed-in bootstrap: where it stands and what a person can do there. */
export type Account = {
  bootstrap: Bootstrap;
  /** Selects one of the user's organizations (/me lists it), remembered for the browser session. */
  choose: (organizationId: string) => void;
  /** Loads /me again after it failed. */
  retry: () => void;
  /** Ends the session; null once the page is leaving, else the problem. */
  signOut: () => Promise<Problem | null>;
};

export type Auth = {
  session: Session | null;
  /** The caller's role in session.organizationId; null while unknown. */
  role: Role | null;
  /** The bootstrap, when the app mounted it; absent for a fixed session (tests, the editor preview) and outside any provider. */
  account?: Account;
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
