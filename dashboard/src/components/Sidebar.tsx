import Link from "next/link";
import {
  LayoutDashboard, Users, Wrench, Settings, Activity, ShieldCheck,
  Brain, Share2, Baby, Home, DollarSign, Zap, History, Sliders, Gauge,
} from "lucide-react";
import type { Me } from "@/lib/graphql";
import { pageEnabled } from "@/lib/packs";

type NavItem = { href: string; label: string; icon: React.ReactNode };
type NavSection = { title: string; items: NavItem[] };

const linkClass =
  "flex items-center px-3 py-2 text-sm font-medium rounded-md text-neutral-600 hover:bg-neutral-50 hover:text-neutral-900";

const sections: NavSection[] = [
  {
    title: "General",
    items: [
      { href: "/", label: "Overview", icon: <LayoutDashboard className="mr-3 h-5 w-5" /> },
      { href: "/users", label: "Users & Access", icon: <Users className="mr-3 h-5 w-5" /> },
      { href: "/sessions", label: "Sessions", icon: <Activity className="mr-3 h-5 w-5" /> },
      { href: "/usage", label: "Usage", icon: <Gauge className="mr-3 h-5 w-5" /> },
    ],
  },
  {
    title: "Knowledge & Family",
    items: [
      { href: "/memory", label: "Memory Explorer", icon: <Brain className="mr-3 h-5 w-5 text-indigo-600" /> },
      { href: "/shared", label: "Shared Spaces", icon: <Share2 className="mr-3 h-5 w-5 text-blue-600" /> },
      { href: "/preferences", label: "Preferences & CRM", icon: <Sliders className="mr-3 h-5 w-5 text-purple-600" /> },
    ],
  },
  {
    title: "Life Domains",
    items: [
      { href: "/baby", label: "Baby Tracker", icon: <Baby className="mr-3 h-5 w-5 text-pink-500" /> },
      { href: "/house", label: "House & Assets", icon: <Home className="mr-3 h-5 w-5 text-amber-600" /> },
      { href: "/finance", label: "Finance & Budgets", icon: <DollarSign className="mr-3 h-5 w-5 text-emerald-600" /> },
    ],
  },
  {
    title: "Control & Audits",
    items: [
      { href: "/automations", label: "Automations & APIs", icon: <Zap className="mr-3 h-5 w-5 text-purple-600" /> },
      { href: "/audit", label: "Audit & Rollback", icon: <History className="mr-3 h-5 w-5 text-red-500" /> },
      { href: "/tools", label: "Tools & Packs", icon: <Wrench className="mr-3 h-5 w-5" /> },
      { href: "/logs", label: "Session Logs", icon: <Activity className="mr-3 h-5 w-5" /> },
      { href: "/oauth", label: "OAuth Connections", icon: <ShieldCheck className="mr-3 h-5 w-5" /> },
      { href: "/settings", label: "Settings", icon: <Settings className="mr-3 h-5 w-5" /> },
    ],
  },
];

/**
 * The dashboard's navigation. Pages of a pack the household has switched off are not listed
 * (V4-27); a section left empty is not shown.
 */
export function Sidebar({ me }: { me: Me | null }) {
  return (
    <nav className="flex-1 p-4 space-y-4 overflow-y-auto">
      {sections.map((section) => {
        const items = section.items.filter((item) => pageEnabled(me, item.href));
        if (items.length === 0) return null;
        return (
          <div key={section.title}>
            <div className="px-3 mb-1 text-xs font-semibold text-neutral-400 uppercase tracking-wider">{section.title}</div>
            <div className="space-y-1">
              {items.map((item) => (
                <Link key={item.href} href={item.href} className={linkClass}>
                  {item.icon}
                  {item.label}
                </Link>
              ))}
            </div>
          </div>
        );
      })}
    </nav>
  );
}
