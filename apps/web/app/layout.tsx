import Link from "next/link";
import type { ReactNode } from "react";

export const metadata = { title: "Geminus" };

// No AuthProvider is mounted until T-008 supplies a real session, so every
// route renders signed out (lib/auth/session.tsx).
export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <header>
          <nav aria-label="Main">
            <Link href="/projects">Projects</Link>
          </nav>
        </header>
        <main>{children}</main>
      </body>
    </html>
  );
}
