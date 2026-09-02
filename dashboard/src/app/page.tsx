import { LayoutDashboard, Users, Wrench, Settings, Activity, ShieldCheck } from "lucide-react";
import Link from "next/link";

export default function Home() {
  return (
    <div className="flex h-screen bg-neutral-100">
      {/* Sidebar */}
      <aside className="w-64 bg-white border-r border-neutral-200 flex flex-col">
        <div className="h-16 flex items-center px-6 border-b border-neutral-200">
          <span className="text-lg font-bold text-neutral-800">MyPA Dashboard</span>
        </div>
        <nav className="flex-1 p-4 space-y-1">
          <Link href="/" className="flex items-center px-3 py-2 text-sm font-medium rounded-md bg-neutral-100 text-neutral-900">
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

      {/* Main Content */}
      <main className="flex-1 overflow-y-auto">
        <header className="h-16 bg-white border-b border-neutral-200 flex items-center px-8 justify-between">
          <h1 className="text-xl font-semibold text-neutral-800">Overview</h1>
        </header>
        <div className="p-8">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6 mb-8">
            <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm">
              <h3 className="text-sm font-medium text-neutral-500">Active Sessions</h3>
              <p className="text-3xl font-bold text-neutral-900 mt-2">12</p>
            </div>
            <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm">
              <h3 className="text-sm font-medium text-neutral-500">Loaded Tools</h3>
              <p className="text-3xl font-bold text-neutral-900 mt-2">8</p>
            </div>
            <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm">
              <h3 className="text-sm font-medium text-neutral-500">Audit Events Today</h3>
              <p className="text-3xl font-bold text-neutral-900 mt-2">1,240</p>
            </div>
          </div>
          <div className="bg-white rounded-xl border border-neutral-200 shadow-sm p-6">
            <h2 className="text-lg font-medium text-neutral-900 mb-4">Welcome to MyPA V3</h2>
            <p className="text-neutral-600">
              The Web Control UI is now running. From here you can manage sessions, configure tools, and view audit logs. 
              Navigation is set up on the left to start building out the individual management screens.
            </p>
          </div>
        </div>
      </main>
    </div>
  );
}
