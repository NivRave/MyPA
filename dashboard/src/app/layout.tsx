import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import { 
  LayoutDashboard, Users, Wrench, Settings, Activity, ShieldCheck,
  Brain, Share2, Baby, Home, DollarSign, Zap, History 
} from "lucide-react";
import Link from "next/link";
import { getMe } from "@/lib/graphql";
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
          <nav className="flex-1 p-4 space-y-4 overflow-y-auto">
            <div>
              <div className="px-3 mb-1 text-xs font-semibold text-neutral-400 uppercase tracking-wider">General</div>
              <div className="space-y-1">
                <Link href="/" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <LayoutDashboard className="mr-3 h-5 w-5" />
                  Overview
                </Link>
                <Link href="/users" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <Users className="mr-3 h-5 w-5" />
                  Users & Access
                </Link>
                <Link href="/sessions" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <Activity className="mr-3 h-5 w-5" />
                  Sessions
                </Link>
              </div>
            </div>

            <div>
              <div className="px-3 mb-1 text-xs font-semibold text-neutral-400 uppercase tracking-wider">Knowledge & Family</div>
              <div className="space-y-1">
                <Link href="/memory" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <Brain className="mr-3 h-5 w-5 text-indigo-600" />
                  Memory Explorer
                </Link>
                <Link href="/shared" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <Share2 className="mr-3 h-5 w-5 text-blue-600" />
                  Shared Spaces
                </Link>
              </div>
            </div>

            <div>
              <div className="px-3 mb-1 text-xs font-semibold text-neutral-400 uppercase tracking-wider">Life Domains</div>
              <div className="space-y-1">
                <Link href="/baby" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <Baby className="mr-3 h-5 w-5 text-pink-500" />
                  Baby Tracker
                </Link>
                <Link href="/house" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <Home className="mr-3 h-5 w-5 text-amber-600" />
                  House & Assets
                </Link>
                <Link href="/finance" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <DollarSign className="mr-3 h-5 w-5 text-emerald-600" />
                  Finance & Budgets
                </Link>
              </div>
            </div>

            <div>
              <div className="px-3 mb-1 text-xs font-semibold text-neutral-400 uppercase tracking-wider">Control & Audits</div>
              <div className="space-y-1">
                <Link href="/automations" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <Zap className="mr-3 h-5 w-5 text-purple-600" />
                  Automations & APIs
                </Link>
                <Link href="/audit" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <History className="mr-3 h-5 w-5 text-red-500" />
                  Audit & Rollback
                </Link>
                <Link href="/tools" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <Wrench className="mr-3 h-5 w-5" />
                  Tools & Plugins
                </Link>
                <Link href="/logs" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <Activity className="mr-3 h-5 w-5" />
                  Session Logs
                </Link>
                <Link href="/oauth" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <ShieldCheck className="mr-3 h-5 w-5" />
                  OAuth Connections
                </Link>
                <Link href="/settings" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
                  <Settings className="mr-3 h-5 w-5" />
                  Settings
                </Link>
              </div>
            </div>
          </nav>
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
