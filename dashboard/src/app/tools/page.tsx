import { revalidatePath } from "next/cache";
import { Blocks, CheckCircle2, XCircle, Lock } from "lucide-react";
import { getMe, graphqlFetch } from "@/lib/graphql";

export const dynamic = "force-dynamic";

const SET_PACK_MUTATION = `
  mutation SetPackEnabled($pack: ID!, $enabled: Boolean!) {
    setPackEnabled(pack: $pack, enabled: $enabled) { id }
  }
`;

/** What Core gives every household, whatever its packs (V4-27). */
const CORE_FEATURES =
  "Memory, reminders, calendar, tasks and contacts, web search, undo, family sharing, your preferences and the personal CRM.";

/**
 * Tools & Packs (V4-27): the household's feature packs, each switched on or off by an owner. A
 * pack that is off adds nothing to chat or to this dashboard; its data is kept.
 */
export default async function ToolsPage() {
  const me = await getMe();
  const isOwner = me?.role === "owner";

  async function setPack(formData: FormData) {
    "use server";
    const pack = (formData.get("pack") as string) || "";
    const enabled = formData.get("enabled") === "true";
    if (!pack) return;
    try {
      await graphqlFetch(SET_PACK_MUTATION, { pack, enabled });
    } catch (err) {
      console.error("setPackEnabled failed:", err);
    }
    // The sidebar lists only the pages of packs that are on.
    revalidatePath("/", "layout");
  }

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      <div className="mb-8">
        <h1 className="text-2xl font-semibold text-neutral-900">Tools & Packs</h1>
        <p className="text-sm text-neutral-500 mt-1">
          Core is always on. Each pack adds its tools, chat guidance, briefing lines and pages for the whole household.
          Switching a pack off hides it; nothing it saved is deleted.
        </p>
      </div>

      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6 mb-6">
        <div className="flex items-center space-x-3 mb-2">
          <div className="p-2 rounded-lg bg-indigo-50 text-indigo-600">
            <Lock className="w-5 h-5" />
          </div>
          <h3 className="font-medium text-neutral-900">Core</h3>
          <span className="text-xs text-neutral-500">Always on</span>
        </div>
        <p className="text-sm text-neutral-600">{CORE_FEATURES}</p>
      </div>

      {!me ? (
        <p className="text-sm text-neutral-600">Sign in to see this household&apos;s packs: send /dashboard to the assistant.</p>
      ) : (
        <>
          {!isOwner && (
            <p className="text-sm text-neutral-500 mb-4">Only an owner of the household can switch packs.</p>
          )}
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {me.household.packs.map((pack) => (
              <div key={pack.id} className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6 flex flex-col" data-pack={pack.id}>
                <div className="flex items-start justify-between mb-4">
                  <div className="flex items-center space-x-3">
                    <div className={`p-2 rounded-lg ${pack.enabled ? "bg-indigo-50 text-indigo-600" : "bg-neutral-100 text-neutral-500"}`}>
                      <Blocks className="w-5 h-5" />
                    </div>
                    <div>
                      <h3 className="font-medium text-neutral-900">{pack.name}</h3>
                      <span className="text-xs text-neutral-500">{pack.enabled ? "On" : "Off"}</span>
                    </div>
                  </div>
                  {pack.enabled ? (
                    <CheckCircle2 className="w-5 h-5 text-green-500" aria-label="on" />
                  ) : (
                    <XCircle className="w-5 h-5 text-neutral-300" aria-label="off" />
                  )}
                </div>

                <p className="text-sm text-neutral-600 flex-1 mb-6">{pack.description}</p>

                {isOwner && (
                  <form action={setPack} className="pt-4 border-t border-neutral-100 flex items-center justify-end">
                    <input type="hidden" name="pack" value={pack.id} />
                    <input type="hidden" name="enabled" value={pack.enabled ? "false" : "true"} />
                    <button
                      type="submit"
                      className={`text-sm font-medium px-3 py-1.5 rounded-md transition-colors ${
                        pack.enabled ? "bg-red-50 text-red-600 hover:bg-red-100" : "bg-indigo-50 text-indigo-600 hover:bg-indigo-100"
                      }`}
                    >
                      {pack.enabled ? `Switch ${pack.name} off` : `Switch ${pack.name} on`}
                    </button>
                  </form>
                )}
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
