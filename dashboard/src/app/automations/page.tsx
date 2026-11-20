import { revalidatePath } from "next/cache";
import { Zap, Globe, CheckCircle2, XCircle, Trash2, Shield } from "lucide-react";
import {
  getMe,
  graphqlFetch,
  type ExternalAPIConnector,
  type AutomationRule,
} from "@/lib/graphql";
import { packOfPage, pageEnabled } from "@/lib/packs";
import { PackOff } from "@/components/PackOff";

export const dynamic = "force-dynamic";

const GET_AUTOMATIONS_QUERY = `
  query GetAutomations {
    externalApiConnectors {
      id
      name
      baseUrl
      defaultMethod
      description
      createdAt
    }
    automationRules {
      id
      name
      triggerType
      triggerTopic
      conditionJson
      actionInstruction
      isActive
      createdAt
    }
  }
`;

const REGISTER_API_MUTATION = `
  mutation RegisterAPI($name: String!, $baseUrl: String!, $defaultMethod: String, $description: String) {
    registerExternalApi(name: $name, baseUrl: $baseUrl, defaultMethod: $defaultMethod, description: $description) { id }
  }
`;

const TOGGLE_RULE_MUTATION = `
  mutation ToggleRule($id: ID!, $isActive: Boolean!) {
    toggleAutomationRule(id: $id, isActive: $isActive) { id }
  }
`;

const DELETE_RULE_MUTATION = `
  mutation DeleteRule($id: ID!) {
    deleteAutomationRule(id: $id)
  }
`;

async function getAutomationData() {
  try {
    const data = await graphqlFetch<{
      externalApiConnectors: ExternalAPIConnector[];
      automationRules: AutomationRule[];
    }>(GET_AUTOMATIONS_QUERY);

    return {
      apis: data.externalApiConnectors || [],
      rules: data.automationRules || [],
    };
  } catch (err) {
    console.error("Failed to load automation data:", err);
    return { apis: [], rules: [] };
  }
}

export default async function AutomationsPage() {
  // V4-27: a switched-off pack's page reads none of its data.
  const me = await getMe();
  if (!pageEnabled(me, "/automations")) {
    return <PackOff name={packOfPage(me?.household.packs ?? [], "/automations")?.name ?? "Automations"} />;
  }
  const { rules } = await getAutomationData();

  async function registerAPI(formData: FormData) {
    "use server";
    const name = (formData.get("name") as string) || "";
    const baseUrl = (formData.get("baseUrl") as string) || "";
    const defaultMethod = (formData.get("defaultMethod") as string) || "GET";
    const description = (formData.get("description") as string) || "";

    try {
      await graphqlFetch(REGISTER_API_MUTATION, {
        name,
        baseUrl,
        defaultMethod,
        description,
      });
    } catch (err) {
      console.error("registerAPI failed:", err);
    }
    revalidatePath("/automations");
  }

  async function toggleRule(formData: FormData) {
    "use server";
    const id = formData.get("id") as string;
    const isActive = formData.get("isActive") === "true";

    try {
      await graphqlFetch(TOGGLE_RULE_MUTATION, { id, isActive: !isActive });
    } catch (err) {
      console.error("toggleRule failed:", err);
    }
    revalidatePath("/automations");
  }

  async function deleteRule(formData: FormData) {
    "use server";
    const id = formData.get("id") as string;

    try {
      await graphqlFetch(DELETE_RULE_MUTATION, { id });
    } catch (err) {
      console.error("deleteRule failed:", err);
    }
    revalidatePath("/automations");
  }

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-semibold text-neutral-900 flex items-center">
            <Zap className="mr-3 h-7 w-7 text-purple-600" />
            On-Demand APIs & User Automations
          </h1>
          <p className="text-sm text-neutral-500 mt-1">
            Dynamic external REST endpoints and event-driven rules: the Automations pack. Preferences and the personal
            CRM are under Preferences &amp; CRM.
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8 mb-8">
        {/* Register External API Connector */}
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6 flex flex-col justify-between">
          <div>
            <div className="flex items-center space-x-2 mb-4">
              <Globe className="w-5 h-5 text-indigo-600" />
              <h2 className="text-base font-semibold text-neutral-900">Register On-Demand External API</h2>
            </div>
            <form action={registerAPI} className="space-y-4 text-xs">
              <div className="grid grid-cols-3 gap-3">
                <div className="col-span-2">
                  <label className="block text-neutral-600 mb-1">Connector Name</label>
                  <input
                    type="text"
                    name="name"
                    placeholder="e.g. municipal_parking, smart_lights"
                    className="w-full border border-neutral-300 rounded p-1.5"
                    required
                  />
                </div>
                <div>
                  <label className="block text-neutral-600 mb-1">Method</label>
                  <select name="defaultMethod" className="w-full border border-neutral-300 rounded p-1.5 bg-white">
                    <option value="GET">GET</option>
                    <option value="POST">POST</option>
                  </select>
                </div>
              </div>
              <div>
                <label className="block text-neutral-600 mb-1">Base URL (Protected against SSRF)</label>
                <input
                  type="url"
                  name="baseUrl"
                  placeholder="https://api.external.com/v1"
                  className="w-full border border-neutral-300 rounded p-1.5 font-mono"
                  required
                />
              </div>
              <div>
                <label className="block text-neutral-600 mb-1">Description / Tool Summary</label>
                <input
                  type="text"
                  name="description"
                  placeholder="Query street parking zones or trigger webhook"
                  className="w-full border border-neutral-300 rounded p-1.5"
                />
              </div>
              <button
                type="submit"
                className="w-full py-2 bg-neutral-900 text-white rounded text-xs font-medium hover:bg-neutral-800 transition-colors"
              >
                Connect API
              </button>
            </form>
          </div>
          <div className="mt-4 p-2.5 bg-neutral-50 rounded text-[11px] text-neutral-500 flex items-center border border-neutral-100">
            <Shield className="w-4 h-4 text-emerald-600 mr-2 shrink-0" />
            Auth headers are encrypted via AES-256-GCM. Internal private IPs (RFC1918) are strictly blocked.
          </div>
        </div>
      </div>

      {/* Active Event-Driven Automation Rules */}
      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden mb-8">
        <div className="p-6 border-b border-neutral-200 flex items-center justify-between">
          <div className="flex items-center space-x-2">
            <Zap className="w-5 h-5 text-purple-600" />
            <h2 className="text-lg font-medium text-neutral-900">Event-Driven Automation Rules (When → If → Then)</h2>
          </div>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="bg-neutral-50 text-neutral-500">
              <tr>
                <th className="px-6 py-3 font-medium">Status</th>
                <th className="px-6 py-3 font-medium">Rule Name</th>
                <th className="px-6 py-3 font-medium">Trigger (When)</th>
                <th className="px-6 py-3 font-medium">Condition (If)</th>
                <th className="px-6 py-3 font-medium">Action Instruction (Then)</th>
                <th className="px-6 py-3 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-200">
              {rules.map((r: AutomationRule) => (
                <tr key={r.id} className="hover:bg-neutral-50">
                  <td className="px-6 py-4">
                    <span className={`inline-flex items-center text-xs font-medium ${r.isActive ? "text-green-600" : "text-neutral-400"}`}>
                      {r.isActive ? <CheckCircle2 className="w-4 h-4 mr-1 text-green-500" /> : <XCircle className="w-4 h-4 mr-1 text-neutral-300" />}
                      {r.isActive ? "Active" : "Disabled"}
                    </span>
                  </td>
                  <td className="px-6 py-4 font-semibold text-neutral-900">{r.name}</td>
                  <td className="px-6 py-4">
                    <span className="text-xs bg-purple-50 text-purple-700 px-2 py-0.5 rounded font-mono">
                      {r.triggerType} ({r.triggerTopic})
                    </span>
                  </td>
                  <td className="px-6 py-4 text-xs font-mono text-neutral-600 max-w-xs truncate">
                    {r.conditionJson}
                  </td>
                  <td className="px-6 py-4 text-xs text-neutral-800 max-w-md">
                    {r.actionInstruction}
                  </td>
                  <td className="px-6 py-4 text-right">
                    <div className="flex items-center justify-end space-x-2">
                      <form action={toggleRule}>
                        <input type="hidden" name="id" value={r.id} />
                        <input type="hidden" name="isActive" value={String(r.isActive)} />
                        <button
                          type="submit"
                          className={`text-xs px-2.5 py-1 rounded font-medium ${
                            r.isActive ? "bg-neutral-100 text-neutral-700 hover:bg-neutral-200" : "bg-green-50 text-green-700 hover:bg-green-100"
                          }`}
                        >
                          {r.isActive ? "Disable" : "Enable"}
                        </button>
                      </form>
                      <form action={deleteRule}>
                        <input type="hidden" name="id" value={r.id} />
                        <button type="submit" className="p-1 text-neutral-400 hover:text-red-600">
                          <Trash2 className="w-4 h-4" />
                        </button>
                      </form>
                    </div>
                  </td>
                </tr>
              ))}
              {rules.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-6 py-8 text-center text-neutral-500">
                    No active automation rules. Example: *&quot;When a large expense is logged, notify my partner in chat&quot;*.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

    </div>
  );
}
