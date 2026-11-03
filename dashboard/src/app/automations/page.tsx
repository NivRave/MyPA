import { revalidatePath } from "next/cache";
import { Zap, Globe, Sliders, Users, Plus, CheckCircle2, XCircle, Trash2, Shield, Clock } from "lucide-react";
import { Pool } from "pg";

export const dynamic = "force-dynamic";

const pool = new Pool({
  connectionString: process.env.DATABASE_URL || "postgres://mypa:mypa_password@localhost:5432/mypa?sslmode=disable",
});

async function getAutomationData(familyGroup: string = "Smith Family", userId: string = "default") {
  try {
    const res = await fetch("http://proxy:8000/graphql", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        query: `
          query GetAutomations($familyGroup: String!, $userId: ID!) {
            externalApiConnectors(familyGroup: $familyGroup) {
              id
              name
              baseUrl
              defaultMethod
              description
              createdAt
            }
            automationRules(familyGroup: $familyGroup) {
              id
              name
              triggerType
              triggerTopic
              conditionJson
              actionInstruction
              isActive
              createdAt
            }
            userPreference(userId: $userId) {
              userId
              preferredLanguage
              tonePersona
              defaultCurrency
              quietHoursStart
              quietHoursEnd
              standingInstructions
            }
            personalCrmContacts(familyGroup: $familyGroup) {
              id
              personName
              relationship
              importantDatesJson
              giftIdeasAndSizes
              medicalNotes
              updatedAt
            }
          }
        `,
        variables: { familyGroup, userId },
      }),
      cache: "no-store",
    });

    if (res.ok) {
      const json = await res.json();
      if (json.data) {
        return {
          apis: json.data.externalApiConnectors || [],
          rules: json.data.automationRules || [],
          preferences: json.data.userPreference || null,
          contacts: json.data.personalCrmContacts || [],
        };
      }
    }
  } catch (err) {}

  try {
    const { rows: aRows } = await pool.query(
      "SELECT * FROM external_api_connectors ORDER BY created_at DESC",
      []
    );
    const { rows: rRows } = await pool.query(
      "SELECT * FROM automation_rules ORDER BY created_at DESC",
      []
    );
    const { rows: pRows } = await pool.query(
      "SELECT * FROM user_preferences LIMIT 1",
      []
    );
    const { rows: cRows } = await pool.query(
      "SELECT * FROM personal_crm_contacts ORDER BY person_name ASC",
      []
    );

    return {
      apis: aRows.map((r: any) => ({
        id: String(r.id),
        name: r.name,
        baseUrl: r.base_url,
        defaultMethod: r.default_method,
        description: r.description,
        createdAt: r.created_at,
      })),
      rules: rRows.map((r: any) => ({
        id: String(r.id),
        name: r.name,
        triggerType: r.trigger_type,
        triggerTopic: r.trigger_topic,
        conditionJson: r.condition_json || "{}",
        actionInstruction: r.action_instruction,
        isActive: !!r.is_active,
        createdAt: r.created_at,
      })),
      preferences: pRows[0]
        ? {
            userId: pRows[0].user_id,
            preferredLanguage: pRows[0].preferred_language,
            tonePersona: pRows[0].tone_persona,
            defaultCurrency: pRows[0].default_currency,
            quietHoursStart: pRows[0].quiet_hours_start,
            quietHoursEnd: pRows[0].quiet_hours_end,
            standingInstructions: pRows[0].standing_instructions,
          }
        : null,
      contacts: cRows.map((r: any) => ({
        id: String(r.id),
        personName: r.person_name,
        relationship: r.relationship,
        importantDatesJson: r.important_dates_json || "{}",
        giftIdeasAndSizes: r.gift_ideas_and_sizes,
        medicalNotes: r.medical_notes,
        updatedAt: r.updated_at,
      })),
    };
  } catch (dbErr) {
    return { apis: [], rules: [], preferences: null, contacts: [] };
  }
}

export default async function AutomationsPage() {
  const { apis, rules, preferences, contacts } = await getAutomationData();

  async function registerAPI(formData: FormData) {
    "use server";
    const name = formData.get("name") as string;
    const baseUrl = formData.get("baseUrl") as string;
    const defaultMethod = formData.get("defaultMethod") as string || "GET";
    const description = formData.get("description") as string || "";

    await fetch("http://proxy:8000/graphql", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        query: `
          mutation RegisterAPI($name: String!, $baseUrl: String!, $defaultMethod: String, $description: String) {
            registerExternalApi(name: $name, baseUrl: $baseUrl, defaultMethod: $defaultMethod, description: $description) { id }
          }
        `,
        variables: { name, baseUrl, defaultMethod, description },
      }),
    });
    revalidatePath("/automations");
  }

  async function toggleRule(formData: FormData) {
    "use server";
    const id = formData.get("id") as string;
    const isActive = formData.get("isActive") === "true";

    await fetch("http://proxy:8000/graphql", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        query: `
          mutation ToggleRule($id: ID!, $isActive: Boolean!) {
            toggleAutomationRule(id: $id, isActive: $isActive) { id }
          }
        `,
        variables: { id, isActive: !isActive },
      }),
    });
    revalidatePath("/automations");
  }

  async function deleteRule(formData: FormData) {
    "use server";
    const id = formData.get("id") as string;
    await fetch("http://proxy:8000/graphql", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        query: `
          mutation DeleteRule($id: ID!) {
            deleteAutomationRule(id: $id)
          }
        `,
        variables: { id },
      }),
    });
    revalidatePath("/automations");
  }

  async function savePreferences(formData: FormData) {
    "use server";
    const preferredLanguage = formData.get("language") as string;
    const tonePersona = formData.get("persona") as string;
    const defaultCurrency = formData.get("currency") as string;
    const quietHoursStart = formData.get("quietStart") as string;
    const quietHoursEnd = formData.get("quietEnd") as string;
    const standingInstructions = formData.get("instructions") as string;

    await fetch("http://proxy:8000/graphql", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        query: `
          mutation SavePref($userId: ID!, $preferredLanguage: String, $tonePersona: String, $defaultCurrency: String, $quietHoursStart: String, $quietHoursEnd: String, $standingInstructions: String) {
            saveUserPreference(userId: $userId, preferredLanguage: $preferredLanguage, tonePersona: $tonePersona, defaultCurrency: $defaultCurrency, quietHoursStart: $quietHoursStart, quietHoursEnd: $quietHoursEnd, standingInstructions: $standingInstructions) { userId }
          }
        `,
        variables: {
          userId: "default",
          preferredLanguage,
          tonePersona,
          defaultCurrency,
          quietHoursStart,
          quietHoursEnd,
          standingInstructions,
        },
      }),
    });
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
            Dynamic external REST endpoints, event-driven rules, runtime persona customization, and personal CRM.
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8 mb-8">
        {/* Runtime Preferences & Standing Instructions */}
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6">
          <div className="flex items-center space-x-2 mb-4">
            <Sliders className="w-5 h-5 text-purple-600" />
            <h2 className="text-base font-semibold text-neutral-900">Runtime Persona & System Prompt</h2>
          </div>
          <form action={savePreferences} className="space-y-4 text-xs">
            <div className="grid grid-cols-3 gap-3">
              <div>
                <label className="block text-neutral-600 mb-1">Language</label>
                <select name="language" defaultValue={preferences?.preferredLanguage || "en"} className="w-full border border-neutral-300 rounded p-1.5 bg-white">
                  <option value="en">English</option>
                  <option value="he">Hebrew (עברית)</option>
                  <option value="es">Spanish</option>
                </select>
              </div>
              <div>
                <label className="block text-neutral-600 mb-1">Tone Persona</label>
                <select name="persona" defaultValue={preferences?.tonePersona || "helpful"} className="w-full border border-neutral-300 rounded p-1.5 bg-white">
                  <option value="helpful">Helpful & Warm</option>
                  <option value="concise">Ultra-Concise</option>
                  <option value="executive">Executive Chief of Staff</option>
                </select>
              </div>
              <div>
                <label className="block text-neutral-600 mb-1">Currency</label>
                <select name="currency" defaultValue={preferences?.defaultCurrency || "USD"} className="w-full border border-neutral-300 rounded p-1.5 bg-white">
                  <option value="USD">USD ($)</option>
                  <option value="ILS">ILS (₪)</option>
                  <option value="EUR">EUR (€)</option>
                </select>
              </div>
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-neutral-600 mb-1 flex items-center">
                  <Clock className="w-3.5 h-3.5 mr-1" /> Quiet Hours Start
                </label>
                <input
                  type="text"
                  name="quietStart"
                  defaultValue={preferences?.quietHoursStart || "23:00"}
                  placeholder="23:00"
                  className="w-full border border-neutral-300 rounded p-1.5"
                />
              </div>
              <div>
                <label className="block text-neutral-600 mb-1 flex items-center">
                  <Clock className="w-3.5 h-3.5 mr-1" /> Quiet Hours End
                </label>
                <input
                  type="text"
                  name="quietEnd"
                  defaultValue={preferences?.quietHoursEnd || "07:00"}
                  placeholder="07:00"
                  className="w-full border border-neutral-300 rounded p-1.5"
                />
              </div>
            </div>

            <div>
              <label className="block text-neutral-600 mb-1">Custom Standing Instructions</label>
              <textarea
                name="instructions"
                rows={3}
                defaultValue={preferences?.standingInstructions || "Always provide concise answers. In morning briefings, highlight baby sleep and bills due."}
                className="w-full border border-neutral-300 rounded p-2 text-xs"
              />
            </div>

            <button
              type="submit"
              className="px-4 py-2 bg-purple-600 text-white rounded text-xs font-medium hover:bg-purple-700 transition-colors"
            >
              Update Preferences
            </button>
          </form>
        </div>

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
              {rules.map((r: any) => (
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
                    No active automation rules. Example: *&quot;When a baby feeding is logged, notify partner on Telegram&quot;*.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Personal CRM Contacts */}
      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
        <div className="p-6 border-b border-neutral-200 flex items-center justify-between">
          <div className="flex items-center space-x-2">
            <Users className="w-5 h-5 text-indigo-600" />
            <h2 className="text-lg font-medium text-neutral-900">Personal & Family CRM</h2>
          </div>
          <span className="text-xs text-neutral-400">Important dates, clothing sizes & medical notes</span>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6 p-6">
          {contacts.map((c: any) => {
            let dates: Record<string, string> = {};
            try {
              dates = JSON.parse(c.importantDatesJson || "{}");
            } catch (_) {}

            return (
              <div key={c.id} className="bg-neutral-50 rounded-xl p-4 border border-neutral-200">
                <div className="flex items-center justify-between mb-2">
                  <h3 className="font-bold text-neutral-900">{c.personName}</h3>
                  <span className="text-xs bg-white border border-neutral-200 px-2 py-0.5 rounded text-neutral-600">
                    {c.relationship}
                  </span>
                </div>
                <div className="space-y-2 text-xs mt-3">
                  {Object.keys(dates).length > 0 && (
                    <div>
                      <span className="text-neutral-500 font-medium">Important Dates:</span>
                      {Object.entries(dates).map(([k, v]) => (
                        <div key={k} className="text-neutral-700 ml-2">
                          • {k}: {v}
                        </div>
                      ))}
                    </div>
                  )}
                  {c.giftIdeasAndSizes && (
                    <div>
                      <span className="text-neutral-500 font-medium">Sizes & Gifts:</span>
                      <p className="text-neutral-700 ml-2">{c.giftIdeasAndSizes}</p>
                    </div>
                  )}
                  {c.medicalNotes && (
                    <div>
                      <span className="text-neutral-500 font-medium">Medical Notes:</span>
                      <p className="text-neutral-700 ml-2">{c.medicalNotes}</p>
                    </div>
                  )}
                </div>
              </div>
            );
          })}
          {contacts.length === 0 && (
            <div className="col-span-3 text-center text-xs text-neutral-500 py-6">
              No contacts registered in Personal CRM yet.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
