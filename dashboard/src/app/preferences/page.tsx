import { revalidatePath } from "next/cache";
import { Sliders, Users, Clock } from "lucide-react";
import {
  graphqlFetch,
  type UserPreference,
  type PersonalCRMContact,
} from "@/lib/graphql";

export const dynamic = "force-dynamic";

// V4-27: preferences and the personal CRM are Core. They were on the Automations page, which a
// household can now switch off; they moved here so that they stay.
const GET_PREFERENCES_QUERY = `
  query GetPreferences($userId: ID!) {
    userPreference(userId: $userId) {
      userId
      preferredLanguage
      tonePersona
      defaultCurrency
      quietHoursStart
      quietHoursEnd
      standingInstructions
    }
    personalCrmContacts {
      id
      personName
      relationship
      importantDatesJson
      giftIdeasAndSizes
      medicalNotes
      updatedAt
    }
  }
`;

const SAVE_PREFERENCES_MUTATION = `
  mutation SavePref(
    $userId: ID!
    $preferredLanguage: String
    $tonePersona: String
    $defaultCurrency: String
    $quietHoursStart: String
    $quietHoursEnd: String
    $standingInstructions: String
  ) {
    saveUserPreference(
      userId: $userId
      preferredLanguage: $preferredLanguage
      tonePersona: $tonePersona
      defaultCurrency: $defaultCurrency
      quietHoursStart: $quietHoursStart
      quietHoursEnd: $quietHoursEnd
      standingInstructions: $standingInstructions
    ) {
      userId
    }
  }
`;

async function getPreferencesData(userId: string = "default") {
  try {
    const data = await graphqlFetch<{
      userPreference: UserPreference | null;
      personalCrmContacts: PersonalCRMContact[];
    }>(GET_PREFERENCES_QUERY, { userId });
    return { preferences: data.userPreference || null, contacts: data.personalCrmContacts || [] };
  } catch (err) {
    console.error("Failed to load preferences:", err);
    return { preferences: null, contacts: [] };
  }
}

export default async function PreferencesPage() {
  const { preferences, contacts } = await getPreferencesData();

  async function savePreferences(formData: FormData) {
    "use server";
    const preferredLanguage = formData.get("language") as string;
    const tonePersona = formData.get("persona") as string;
    const defaultCurrency = formData.get("currency") as string;
    const quietHoursStart = formData.get("quietStart") as string;
    const quietHoursEnd = formData.get("quietEnd") as string;
    const standingInstructions = formData.get("instructions") as string;

    try {
      await graphqlFetch(SAVE_PREFERENCES_MUTATION, {
        userId: "default",
        preferredLanguage,
        tonePersona,
        defaultCurrency,
        quietHoursStart,
        quietHoursEnd,
        standingInstructions,
      });
    } catch (err) {
      console.error("savePreferences failed:", err);
    }
    revalidatePath("/preferences");
  }

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      <div className="mb-8">
        <h1 className="text-2xl font-semibold text-neutral-900 flex items-center">
          <Sliders className="mr-3 h-7 w-7 text-purple-600" />
          Preferences & Personal CRM
        </h1>
        <p className="text-sm text-neutral-500 mt-1">
          How the assistant speaks to you, your quiet hours and standing instructions, and the family&apos;s important dates.
        </p>
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
                defaultValue={preferences?.standingInstructions || "Always provide concise answers. In morning briefings, highlight what is due today."}
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
          {contacts.map((c: PersonalCRMContact) => {
            let dates: Record<string, string> = {};
            try {
              dates = JSON.parse(c.importantDatesJson || "{}");
            } catch {}

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
