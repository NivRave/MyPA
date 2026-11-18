import { revalidatePath } from "next/cache";
import { Baby, Heart, Scale, Ruler, AlertCircle, Plus, Sparkles, Check, Package, Clock } from "lucide-react";
import {
  graphqlFetch,
  getDashboardFamilyGroup,
  type BabyProfile,
  type BabyCareLog,
  type BabyInventoryItem,
} from "@/lib/graphql";

export const dynamic = "force-dynamic";

function calculateAge(dob: Date, now: Date): string {
  const diffTime = Math.abs(now.getTime() - dob.getTime());
  const diffDays = Math.floor(diffTime / (1000 * 60 * 60 * 24));
  
  if (diffDays < 14) {
    return `${diffDays} days old`;
  }
  const weeks = Math.floor(diffDays / 7);
  if (weeks < 16) {
    return `${weeks} weeks old (${diffDays} days)`;
  }
  const months = Math.floor(diffDays / 30.4375);
  const remainingDays = Math.floor(diffDays % 30.4375);
  return `${months} months, ${remainingDays} days old`;
}

function getMilestones(dob: Date, now: Date): string[] {
  const diffDays = Math.floor(Math.abs(now.getTime() - dob.getTime()) / (1000 * 60 * 60 * 24));
  const months = diffDays / 30.4375;

  if (months < 2) {
    return [
      "2-Month Vaccination checkup (DTaP, IPV, Hib, PCV13, Rotavirus)",
      "Tracks objects with eyes & starts smiling in response to voices",
      "Feedings: 80–120 ml every 2.5–3.5 hours",
      "Safe sleep: Always on back, firm mattress with zero loose bedding",
    ];
  } else if (months < 4) {
    return [
      "4-Month Pediatrician checkup & vaccine booster window",
      "Pushes up on elbows during tummy time & holds head steady",
      "Discovers hands and begins reaching for dangling toys",
      "Sleep consolidation: longer stretches at night (4–6 hours)",
    ];
  } else if (months < 6) {
    return [
      "6-Month Pediatrician Well-Baby checkup & milestone screening",
      "Introduction to solid foods readiness (iron-rich purees/cereals)",
      "Rolls over tummy-to-back and back-to-tummy",
      "Responds to own name and strings vowel sounds together",
    ];
  } else {
    return [
      "9–12 Month Developmental milestone screening (standing, pincer grasp)",
      "Finger foods and self-feeding with soft cut vegetables and proteins",
      "Separation anxiety peak & object permanence emerging",
      "Hydration with small amounts of water in a sippy cup",
    ];
  }
}

const GET_BABY_DATA_QUERY = `
  query GetBabyData($familyGroup: String!) {
    babyProfiles(familyGroup: $familyGroup) {
      id
      name
      dateOfBirth
      gender
      birthWeightGrams
      currentWeightGrams
      heightCm
      pediatricianInfo
      allergiesNotes
      updatedAt
    }
    babyInventory(familyGroup: $familyGroup) {
      id
      itemName
      quantityRemaining
      unit
      lowThreshold
      updatedAt
    }
  }
`;

const GET_BABY_LOGS_QUERY = `
  query GetBabyLogs($babyId: ID!) {
    babyCareLogs(babyId: $babyId, limit: 30) {
      id
      logType
      valueNumeric
      unit
      details
      timestamp
      loggedByUserId
    }
  }
`;

const LOG_CARE_MUTATION = `
  mutation LogCare($babyId: ID!, $familyGroup: String!, $logType: String!, $valueNumeric: Float, $unit: String, $details: String) {
    logBabyCare(babyId: $babyId, familyGroup: $familyGroup, logType: $logType, valueNumeric: $valueNumeric, unit: $unit, details: $details) { id }
  }
`;

const UPDATE_INVENTORY_MUTATION = `
  mutation UpdateInv($familyGroup: String!, $itemName: String!, $quantityRemaining: Float!, $unit: String!, $lowThreshold: Float!) {
    updateBabyInventory(familyGroup: $familyGroup, itemName: $itemName, quantityRemaining: $quantityRemaining, unit: $unit, lowThreshold: $lowThreshold) { id }
  }
`;

async function getBabyData(familyGroup: string = getDashboardFamilyGroup()): Promise<{
  profile: BabyProfile | null;
  logs: BabyCareLog[];
  inventory: BabyInventoryItem[];
  dob: Date | null;
  ageString: string;
  milestoneSuggestions: string[];
}> {
  const now = new Date();
  try {
    const data = await graphqlFetch<{
      babyProfiles: BabyProfile[];
      babyInventory: BabyInventoryItem[];
    }>(GET_BABY_DATA_QUERY, { familyGroup });

    const baby = data.babyProfiles?.[0] || null;
    let logs: BabyCareLog[] = [];
    if (baby) {
      const logData = await graphqlFetch<{ babyCareLogs: BabyCareLog[] }>(
        GET_BABY_LOGS_QUERY,
        { babyId: baby.id }
      );
      logs = logData.babyCareLogs || [];
    }

    const dob = baby ? new Date(baby.dateOfBirth) : null;
    return {
      profile: baby,
      logs,
      inventory: data.babyInventory || [],
      dob,
      ageString: dob ? calculateAge(dob, now) : "No profile registered",
      milestoneSuggestions: dob ? getMilestones(dob, now) : [],
    };
  } catch (err) {
    console.error("Failed to load baby data:", err);
    return {
      profile: null,
      logs: [],
      inventory: [],
      dob: null,
      ageString: "No profile registered",
      milestoneSuggestions: [],
    };
  }
}

export default async function BabyTrackerPage() {
  const familyGroup = getDashboardFamilyGroup();
  const { profile, logs, inventory, dob, ageString, milestoneSuggestions } = await getBabyData(familyGroup);

  async function logCare(formData: FormData) {
    "use server";
    const logType = (formData.get("logType") as string) || "";
    const value = parseFloat((formData.get("value") as string) || "0") || 0;
    const unit = (formData.get("unit") as string) || "";
    const details = (formData.get("details") as string) || "";
    const babyId = (formData.get("babyId") as string) || "";
    if (!babyId) {
      console.warn("logCare aborted: no baby profile registered");
      return;
    }

    try {
      await graphqlFetch(LOG_CARE_MUTATION, {
        babyId,
        familyGroup: getDashboardFamilyGroup(),
        logType,
        valueNumeric: value,
        unit,
        details,
      });
    } catch (err) {
      console.error("logCare failed:", err);
    }
    revalidatePath("/baby");
  }

  async function updateStock(formData: FormData) {
    "use server";
    const itemName = (formData.get("itemName") as string) || "";
    const qty = parseFloat((formData.get("quantity") as string) || "0") || 0;
    const unit = (formData.get("unit") as string) || "pcs";
    const lowThreshold = parseFloat((formData.get("threshold") as string) || "5") || 5;

    try {
      await graphqlFetch(UPDATE_INVENTORY_MUTATION, {
        familyGroup: getDashboardFamilyGroup(),
        itemName,
        quantityRemaining: qty,
        unit,
        lowThreshold,
      });
    } catch (err) {
      console.error("updateStock failed:", err);
    }
    revalidatePath("/baby");
  }

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-semibold text-neutral-900 flex items-center">
            <Baby className="mr-3 h-7 w-7 text-pink-500" />
            Baby & Family Care Tracker
          </h1>
          <p className="text-sm text-neutral-500 mt-1">
            Dynamic age computation, care routines, medication cooldowns, and proactive developmental milestones.
          </p>
        </div>
      </div>

      {/* Child Profile Card */}
      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6 mb-8">
        {profile ? (
          <div className="flex flex-col md:flex-row md:items-center justify-between gap-6">
            <div className="flex items-center space-x-4">
              <div className="w-16 h-16 rounded-full bg-pink-100 flex items-center justify-center text-pink-600 text-2xl font-bold">
                {profile.name[0]}
              </div>
              <div>
                <h2 className="text-xl font-bold text-neutral-900">{profile.name}</h2>
                <p className="text-sm font-medium text-pink-600">{ageString}</p>
                <p className="text-xs text-neutral-400 mt-0.5">
                  DOB: {dob?.toLocaleDateString()} • Gender: {profile.gender}
                </p>
              </div>
            </div>

            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-center">
              <div className="p-3 bg-neutral-50 rounded-lg">
                <Scale className="w-4 h-4 mx-auto text-neutral-500 mb-1" />
                <div className="text-xs text-neutral-500">Weight</div>
                <div className="text-sm font-bold text-neutral-800">
                  {profile.currentWeightGrams ? `${(profile.currentWeightGrams / 1000).toFixed(2)} kg` : "—"}
                </div>
              </div>
              <div className="p-3 bg-neutral-50 rounded-lg">
                <Ruler className="w-4 h-4 mx-auto text-neutral-500 mb-1" />
                <div className="text-xs text-neutral-500">Height</div>
                <div className="text-sm font-bold text-neutral-800">
                  {profile.heightCm ? `${profile.heightCm} cm` : "—"}
                </div>
              </div>
              <div className="p-3 bg-neutral-50 rounded-lg">
                <Heart className="w-4 h-4 mx-auto text-neutral-500 mb-1" />
                <div className="text-xs text-neutral-500">Pediatrician</div>
                <div className="text-sm font-bold text-neutral-800 truncate max-w-[100px]" title={profile.pediatricianInfo || "None"}>
                  {profile.pediatricianInfo || "—"}
                </div>
              </div>
              <div className="p-3 bg-neutral-50 rounded-lg">
                <AlertCircle className="w-4 h-4 mx-auto text-neutral-500 mb-1" />
                <div className="text-xs text-neutral-500">Allergies</div>
                <div className="text-sm font-bold text-neutral-800">
                  {profile.allergiesNotes || "None"}
                </div>
              </div>
            </div>
          </div>
        ) : (
          <div className="flex flex-col md:flex-row items-center justify-between gap-4 py-2">
            <div className="flex items-center space-x-4">
              <div className="w-14 h-14 rounded-full bg-neutral-100 flex items-center justify-center text-neutral-400">
                <Baby className="w-7 h-7" />
              </div>
              <div>
                <h2 className="text-lg font-semibold text-neutral-800">No Baby Profile Registered</h2>
                <p className="text-xs text-neutral-500 mt-0.5">
                  Register a child profile via chat (e.g. &quot;Our baby Maya was born on...&quot;) to start tracking growth, care logs, and pediatric milestones.
                </p>
              </div>
            </div>
            <div className="text-xs px-3 py-1.5 bg-neutral-100 text-neutral-600 rounded-md font-medium">
              Family: {familyGroup}
            </div>
          </div>
        )}
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-8 mb-8">
        {/* Quick Log Form */}
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6">
          <h2 className="text-base font-semibold text-neutral-900 mb-4 flex items-center">
            <Plus className="w-5 h-5 mr-2 text-pink-500" />
            Log Care Routine
          </h2>
          <form action={logCare} className="space-y-4 text-sm">
            <input type="hidden" name="babyId" value={profile?.id || ""} />
            <div>
              <label className="block text-xs font-medium text-neutral-700 mb-1">Routine Type</label>
              <select name="logType" className="w-full border border-neutral-300 rounded-md p-2 text-sm bg-white">
                <option value="feeding">Feeding (Formula / Breastmilk)</option>
                <option value="sleep">Sleep (Nap / Night)</option>
                <option value="diaper">Diaper (Wet / Dirty)</option>
                <option value="temperature">Temperature (°C)</option>
                <option value="medication">Medication (Paracetamol / Drops)</option>
                <option value="milestone">Milestone Achieved</option>
              </select>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-xs font-medium text-neutral-700 mb-1">Amount / Value</label>
                <input
                  type="number"
                  step="any"
                  name="value"
                  placeholder="e.g. 120"
                  className="w-full border border-neutral-300 rounded-md p-2 text-sm"
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-neutral-700 mb-1">Unit</label>
                <input
                  type="text"
                  name="unit"
                  placeholder="ml, min, °C"
                  className="w-full border border-neutral-300 rounded-md p-2 text-sm"
                />
              </div>
            </div>
            <div>
              <label className="block text-xs font-medium text-neutral-700 mb-1">Details / Notes</label>
              <input
                type="text"
                name="details"
                placeholder="e.g. Finished bottle quickly, happy mood"
                className="w-full border border-neutral-300 rounded-md p-2 text-sm"
              />
            </div>
            <button
              type="submit"
              disabled={!profile}
              className={`w-full py-2 rounded-md text-sm font-medium transition-colors ${
                profile
                  ? "bg-pink-600 text-white hover:bg-pink-700"
                  : "bg-neutral-200 text-neutral-400 cursor-not-allowed"
              }`}
            >
              {profile ? "Save Care Log" : "Register Child Profile First"}
            </button>
          </form>
        </div>

        {/* Proactive Milestone Suggestions */}
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6 flex flex-col">
          <div className="flex items-center space-x-2 mb-4">
            <Sparkles className="w-5 h-5 text-amber-500" />
            <h2 className="text-base font-semibold text-neutral-900">
              Age-Based Suggestions ({ageString})
            </h2>
          </div>
          <div className="space-y-3 flex-1 overflow-y-auto">
            {milestoneSuggestions.length > 0 ? (
              milestoneSuggestions.map((m, idx) => (
                <div key={idx} className="p-3 bg-amber-50/60 border border-amber-100 rounded-lg flex items-start space-x-3 text-xs text-amber-900">
                  <Check className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
                  <span>{m}</span>
                </div>
              ))
            ) : (
              <div className="text-xs text-neutral-400 text-center py-8">
                Age-specific developmental milestones and vaccination schedules will appear here once a baby profile is registered.
              </div>
            )}
          </div>
        </div>

        {/* Baby Inventory Supplies */}
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6 flex flex-col">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-base font-semibold text-neutral-900 flex items-center">
              <Package className="w-5 h-5 mr-2 text-indigo-500" />
              Supplies & Restock
            </h2>
          </div>
          <div className="space-y-3 flex-1 overflow-y-auto mb-4">
            {inventory.map((item: BabyInventoryItem) => {
              const isLow = item.quantityRemaining <= item.lowThreshold;
              return (
                <div key={item.id} className="p-3 bg-neutral-50 rounded-lg flex items-center justify-between text-xs">
                  <div>
                    <span className="font-semibold text-neutral-800">{item.itemName}</span>
                    <div className="text-[11px] text-neutral-400">
                      Threshold: {item.lowThreshold} {item.unit}
                    </div>
                  </div>
                  <div className="text-right">
                    <span className={`font-bold ${isLow ? "text-red-600" : "text-green-600"}`}>
                      {item.quantityRemaining} {item.unit}
                    </span>
                    {isLow && (
                      <span className="block text-[10px] text-red-500 font-medium">⚠️ Low Stock</span>
                    )}
                  </div>
                </div>
              );
            })}
            {inventory.length === 0 && (
              <div className="text-xs text-neutral-400 text-center py-6">
                Formula, diapers, and Vitamin D drops inventory will appear here.
              </div>
            )}
          </div>

          <form action={updateStock} className="flex gap-2">
            <input
              type="text"
              name="itemName"
              placeholder="Item name"
              className="border border-neutral-300 rounded p-1.5 text-xs flex-1"
              required
            />
            <input
              type="number"
              step="any"
              name="quantity"
              placeholder="Qty"
              className="border border-neutral-300 rounded p-1.5 text-xs w-16"
              required
            />
            <button
              type="submit"
              className="px-3 py-1.5 bg-neutral-900 text-white rounded text-xs font-medium hover:bg-neutral-800"
            >
              Update
            </button>
          </form>
        </div>
      </div>

      {/* Routine Logs Timeline */}
      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
        <div className="p-6 border-b border-neutral-200">
          <h2 className="text-lg font-medium text-neutral-900">Recent Care Routine Activity</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="bg-neutral-50 text-neutral-500">
              <tr>
                <th className="px-6 py-3 font-medium">Time</th>
                <th className="px-6 py-3 font-medium">Type</th>
                <th className="px-6 py-3 font-medium">Value / Amount</th>
                <th className="px-6 py-3 font-medium">Details</th>
                <th className="px-6 py-3 font-medium">Logged By</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-200">
              {logs.map((log: BabyCareLog) => (
                <tr key={log.id} className="hover:bg-neutral-50">
                  <td className="px-6 py-4 whitespace-nowrap text-xs text-neutral-500">
                    <span className="inline-flex items-center">
                      <Clock className="w-3.5 h-3.5 mr-1 text-neutral-400" />
                      {new Date(log.timestamp).toLocaleString()}
                    </span>
                  </td>
                  <td className="px-6 py-4">
                    <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium capitalize bg-pink-50 text-pink-700">
                      {log.logType}
                    </span>
                  </td>
                  <td className="px-6 py-4 font-semibold text-neutral-900">
                    {log.valueNumeric > 0 ? `${log.valueNumeric} ${log.unit}` : "—"}
                  </td>
                  <td className="px-6 py-4 text-neutral-600 max-w-sm truncate">
                    {log.details || "—"}
                  </td>
                  <td className="px-6 py-4 text-xs text-neutral-400">
                    {log.loggedByUserId}
                  </td>
                </tr>
              ))}
              {logs.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-6 py-8 text-center text-neutral-500">
                    No care routines logged yet. Use voice/chat or the quick-log form above.
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
