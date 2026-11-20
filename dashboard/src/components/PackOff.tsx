import Link from "next/link";
import { PackageX } from "lucide-react";

/**
 * Shown instead of a pack's page while the household has the pack off (V4-27). The page reads
 * none of the pack's data; the data itself is kept.
 */
export function PackOff({ name }: { name: string }) {
  return (
    <div className="flex-1 p-8 overflow-y-auto">
      <div className="max-w-xl mx-auto mt-16 bg-white rounded-xl border border-neutral-200 p-8 text-center">
        <PackageX className="w-10 h-10 text-neutral-400 mx-auto mb-4" />
        <h1 className="text-lg font-semibold text-neutral-900">The {name} pack is switched off</h1>
        <p className="text-sm text-neutral-600 mt-2">
          This household has the {name} pack off, so the assistant offers none of its features. Anything saved
          before is kept. An owner can switch it on under{" "}
          <Link href="/tools" className="underline">
            Tools &amp; Packs
          </Link>
          .
        </p>
      </div>
    </div>
  );
}
