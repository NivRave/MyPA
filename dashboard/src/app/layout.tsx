import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import Link from "next/link";
import { getMe } from "@/lib/graphql";
import { Sidebar } from "@/components/Sidebar";
import "./globals.css";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "MyPA Dashboard",
  description: "Web Control UI for MyPA",
};

export default async function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  // V4-35: who is signed in. Without a valid session the proxy sends a page to /signin; a session
  // that ended meanwhile shows here, with the way back in.
  const me = await getMe();
  return (
    <html lang="en" className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}>
      <body className="min-h-full flex h-screen bg-neutral-100">
        {/* Sidebar */}
        <aside className="w-64 bg-white border-r border-neutral-200 flex flex-col shrink-0">
          <div className="h-16 flex items-center px-6 border-b border-neutral-200">
            <span className="text-lg font-bold text-neutral-800">MyPA Dashboard</span>
          </div>
          {/* V4-27: pages of a switched-off pack are not listed. */}
          <Sidebar me={me} />
        </aside>

        {/* Main Content Wrapper */}
        <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
          <div className="h-12 shrink-0 flex items-center justify-end gap-4 px-6 border-b border-neutral-200 bg-white text-sm">
            {me ? (
              <>
                <span className="text-neutral-600">
                  {me.name || "Signed in"} · <span className="capitalize">{me.role}</span>
                  {me.isOperator ? " · operator" : ""}
                </span>
                <form action="/api/auth/signout" method="post">
                  <button type="submit" className="text-neutral-500 hover:text-neutral-900 underline">Sign out</button>
                </form>
              </>
            ) : (
              <Link href="/signin" className="text-neutral-600 underline">Not signed in: get a link with /dashboard</Link>
            )}
          </div>
          {children}
        </div>
      </body>
    </html>
  );
}
