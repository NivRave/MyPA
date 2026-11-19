import Link from "next/link";
import { 
  Brain, Share2, Baby, Home as HomeIcon, DollarSign, Zap, History,
  ArrowRight, Users
} from "lucide-react";

export default function Home() {
  const domains = [
    {
      title: "Memory Explorer",
      desc: "Topic-isolated memories, living entity cards & conflict resolution.",
      href: "/memory",
      icon: Brain,
      color: "text-indigo-600 bg-indigo-50",
      badge: "Topic 2",
    },
    {
      title: "Shared Spaces",
      desc: "Partner sync, cross-user notifications & collaborative claim/complete tasks.",
      href: "/shared",
      icon: Share2,
      color: "text-blue-600 bg-blue-50",
      badge: "Topic 9",
    },
    {
      title: "Baby & Family Care",
      desc: "Dynamic age engine, feeding/sleep logs, and proactive milestone suggestions.",
      href: "/baby",
      icon: Baby,
      color: "text-pink-600 bg-pink-50",
      badge: "Topic 4",
    },
    {
      title: "House & Assets",
      desc: "Lease/warranty registry, advance renewal alerts, and vehicle maintenance intervals.",
      href: "/house",
      icon: HomeIcon,
      color: "text-amber-600 bg-amber-50",
      badge: "Topic 5 & 8.1",
    },
    {
      title: "Finance & Budgets",
      desc: "Expense tracking, receipt vision flows, monthly category budgets & bill spikes.",
      href: "/finance",
      icon: DollarSign,
      color: "text-emerald-600 bg-emerald-50",
      badge: "Topic 6",
    },
    {
      title: "Automations & APIs",
      desc: "On-demand REST/webhook connectors, When→If→Then rules, and Personal CRM.",
      href: "/automations",
      icon: Zap,
      color: "text-purple-600 bg-purple-50",
      badge: "Topics 7 & 8",
    },
    {
      title: "Audit & Rollback",
      desc: "Immutable mutation audit log with previous-state snapshots and one-click rollback.",
      href: "/audit",
      icon: History,
      color: "text-red-600 bg-red-50",
      badge: "Topic 1",
    },
    {
      title: "Users & Pairing",
      desc: "Manage chat users, family groups, and active 6-digit pairing codes.",
      href: "/users",
      icon: Users,
      color: "text-cyan-600 bg-cyan-50",
      badge: "Core Access",
    },
  ];

  return (
    <>
      <header className="h-16 bg-white border-b border-neutral-200 flex items-center px-8 justify-between shrink-0">
        <h1 className="text-xl font-semibold text-neutral-800">MyPA System Overview</h1>
        <div className="flex items-center space-x-2 text-xs text-neutral-500 font-mono">
          <span className="w-2 h-2 rounded-full bg-green-500 inline-block"></span>
          <span>Orchestrator V3 • Active</span>
        </div>
      </header>

      <div className="p-8 overflow-y-auto flex-1">
        {/* KPI Summary Cards */}
        <div className="grid grid-cols-1 md:grid-cols-4 gap-6 mb-8">
          <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm">
            <h3 className="text-xs font-semibold text-neutral-400 uppercase tracking-wider">Active Domains</h3>
            <p className="text-3xl font-bold text-neutral-900 mt-2">7</p>
            <span className="text-xs text-indigo-600 font-medium">Baby, House, Finance & more</span>
          </div>
          <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm">
            <h3 className="text-xs font-semibold text-neutral-400 uppercase tracking-wider">Memory Scope</h3>
            <p className="text-3xl font-bold text-neutral-900 mt-2">Topic-Mapped</p>
            <span className="text-xs text-green-600 font-medium">Automatic superseding active</span>
          </div>
          <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm">
            <h3 className="text-xs font-semibold text-neutral-400 uppercase tracking-wider">Family Collaboration</h3>
            <p className="text-3xl font-bold text-neutral-900 mt-2">Multi-User</p>
            <span className="text-xs text-blue-600 font-medium">Cross-partner push sync</span>
          </div>
          <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm">
            <h3 className="text-xs font-semibold text-neutral-400 uppercase tracking-wider">Durability & Audit</h3>
            <p className="text-3xl font-bold text-neutral-900 mt-2">100%</p>
            <span className="text-xs text-emerald-600 font-medium">One-click rollback enabled</span>
          </div>
        </div>

        {/* Domain Navigation Grid */}
        <div className="mb-8">
          <h2 className="text-lg font-semibold text-neutral-900 mb-4">Feature Domains & Control Panels</h2>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
            {domains.map((d) => {
              const Icon = d.icon;
              return (
                <Link
                  key={d.href}
                  href={d.href}
                  className="bg-white p-5 rounded-xl border border-neutral-200 shadow-sm hover:shadow-md hover:border-neutral-300 transition-all flex flex-col justify-between group"
                >
                  <div>
                    <div className="flex items-center justify-between mb-3">
                      <div className={`p-2.5 rounded-lg ${d.color}`}>
                        <Icon className="w-5 h-5" />
                      </div>
                      <span className="text-[10px] font-semibold uppercase px-2 py-0.5 rounded bg-neutral-100 text-neutral-600">
                        {d.badge}
                      </span>
                    </div>
                    <h3 className="font-semibold text-neutral-900 group-hover:text-indigo-600 transition-colors">
                      {d.title}
                    </h3>
                    <p className="text-xs text-neutral-500 mt-1.5 leading-relaxed">
                      {d.desc}
                    </p>
                  </div>
                  <div className="mt-4 pt-3 border-t border-neutral-100 flex items-center justify-between text-xs font-medium text-neutral-600 group-hover:text-indigo-600">
                    <span>Manage</span>
                    <ArrowRight className="w-3.5 h-3.5 transform group-hover:translate-x-1 transition-transform" />
                  </div>
                </Link>
              );
            })}
          </div>
        </div>
      </div>
    </>
  );
}
