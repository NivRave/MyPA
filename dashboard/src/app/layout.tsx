import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import { LayoutDashboard, Users, Wrench, Settings, Activity, ShieldCheck } from "lucide-react";
import Link from "next/link";
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

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}>
      <body className="min-h-full flex h-screen bg-neutral-100">
        {/* Sidebar */}
        <aside className="w-64 bg-white border-r border-neutral-200 flex flex-col shrink-0">
          <div className="h-16 flex items-center px-6 border-b border-neutral-200">
            <span className="text-lg font-bold text-neutral-800">MyPA Dashboard</span>
          </div>
          <nav className="flex-1 p-4 space-y-1">
            <Link href="/" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
              <LayoutDashboard className="mr-3 h-5 w-5" />
              Overview
            </Link>
            <Link href="/sessions" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
              <Users className="mr-3 h-5 w-5" />
              Sessions
            </Link>
            <Link href="/tools" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
              <Wrench className="mr-3 h-5 w-5" />
              Tools & Plugins
            </Link>
            <Link href="/logs" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
              <Activity className="mr-3 h-5 w-5" />
              Audit Logs
            </Link>
            <Link href="/oauth" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
              <ShieldCheck className="mr-3 h-5 w-5" />
              OAuth Connections
            </Link>
            <Link href="/settings" className="flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900">
              <Settings className="mr-3 h-5 w-5" />
              Settings
            </Link>
          </nav>
        </aside>

        {/* Main Content Wrapper */}
        <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
          {children}
        </div>
      </body>
    </html>
  );
}
