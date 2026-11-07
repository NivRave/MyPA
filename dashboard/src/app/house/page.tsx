import { revalidatePath } from "next/cache";
import { Home, FileText, Wrench, Car, AlertTriangle, CheckCircle, Plus } from "lucide-react";
import {
  graphqlFetch,
  type HouseContractRecord,
  type HomeMaintenanceItem,
  type VehicleAsset,
} from "@/lib/graphql";

export const dynamic = "force-dynamic";

const GET_HOUSE_DATA_QUERY = `
  query GetHouseData($familyGroup: String!) {
    houseContracts(familyGroup: $familyGroup) {
      id
      recordType
      title
      vendorOrLandlord
      amount
      currency
      expirationOrRenewalDate
      noticePeriodDays
      createdAt
    }
    homeMaintenanceItems(familyGroup: $familyGroup) {
      id
      taskName
      category
      intervalDays
      lastCompletedAt
      nextDueAt
      contractorInfo
      notes
    }
    vehicleAssets(familyGroup: $familyGroup) {
      id
      name
      licensePlate
      insuranceExpiry
      annualTestExpiry
      currentMileageKm
      nextServiceMileageKm
      notes
    }
  }
`;

const COMPLETE_MAINTENANCE_MUTATION = `
  mutation CompleteMaintenance($id: ID!) {
    completeHomeMaintenance(id: $id) { id }
  }
`;

const SAVE_CONTRACT_MUTATION = `
  mutation SaveContract(
    $familyGroup: String!
    $recordType: String!
    $title: String!
    $vendorOrLandlord: String
    $amount: Float
    $currency: String
    $expirationOrRenewalDate: Time
    $noticePeriodDays: Int
  ) {
    saveHouseContract(
      familyGroup: $familyGroup
      recordType: $recordType
      title: $title
      vendorOrLandlord: $vendorOrLandlord
      amount: $amount
      currency: $currency
      expirationOrRenewalDate: $expirationOrRenewalDate
      noticePeriodDays: $noticePeriodDays
    ) {
      id
    }
  }
`;

async function getHouseData(familyGroup: string = "Smith Family"): Promise<{
  contracts: HouseContractRecord[];
  maintenance: HomeMaintenanceItem[];
  vehicles: VehicleAsset[];
}> {
  try {
    const data = await graphqlFetch<{
      houseContracts: HouseContractRecord[];
      homeMaintenanceItems: HomeMaintenanceItem[];
      vehicleAssets: VehicleAsset[];
    }>(GET_HOUSE_DATA_QUERY, { familyGroup });

    return {
      contracts: data.houseContracts || [],
      maintenance: data.homeMaintenanceItems || [],
      vehicles: data.vehicleAssets || [],
    };
  } catch (err) {
    console.error("Failed to load house data:", err);
    return { contracts: [], maintenance: [], vehicles: [] };
  }
}

export default async function HousePage() {
  const familyGroup = "Smith Family";
  const { contracts, maintenance, vehicles } = await getHouseData(familyGroup);

  async function completeTask(formData: FormData) {
    "use server";
    const id = formData.get("id") as string;
    try {
      await graphqlFetch(COMPLETE_MAINTENANCE_MUTATION, { id });
    } catch (err) {
      console.error("completeTask failed:", err);
    }
    revalidatePath("/house");
  }

  async function addContract(formData: FormData) {
    "use server";
    const recordType = (formData.get("recordType") as string) || "warranty";
    const title = (formData.get("title") as string) || "";
    const vendor = (formData.get("vendor") as string) || "";
    const amount = parseFloat((formData.get("amount") as string) || "0") || 0;
    const currency = (formData.get("currency") as string) || "USD";
    const expiry = formData.get("expiry") ? new Date(formData.get("expiry") as string) : null;
    const noticeDays = parseInt((formData.get("noticeDays") as string) || "30", 10) || 30;

    try {
      await graphqlFetch(SAVE_CONTRACT_MUTATION, {
        familyGroup: "Smith Family",
        recordType,
        title,
        vendorOrLandlord: vendor,
        amount,
        currency,
        expirationOrRenewalDate: expiry,
        noticePeriodDays: noticeDays,
      });
    } catch (err) {
      console.error("addContract failed:", err);
    }
    revalidatePath("/house");
  }

  const now = new Date();

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-semibold text-neutral-900 flex items-center">
            <Home className="mr-3 h-7 w-7 text-amber-600" />
            House Maintenance, Contracts & Vehicle Vault
          </h1>
          <p className="text-sm text-neutral-500 mt-1">
            Centralized document vault, preventive home maintenance schedules, and vehicle assets.
          </p>
        </div>
      </div>

      {/* Contracts & Warranties Section */}
      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden mb-8">
        <div className="p-6 border-b border-neutral-200 flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div className="flex items-center space-x-2">
            <FileText className="w-5 h-5 text-amber-600" />
            <h2 className="text-lg font-medium text-neutral-900">Contracts, Leases & Warranties</h2>
          </div>
          <form action={addContract} className="flex flex-wrap gap-2 text-xs">
            <select name="recordType" className="border border-neutral-300 rounded p-1.5 bg-white">
              <option value="warranty">Warranty</option>
              <option value="lease">Lease</option>
              <option value="insurance">Insurance</option>
              <option value="utility">Utility</option>
              <option value="receipt">Receipt</option>
            </select>
            <input
              type="text"
              name="title"
              placeholder="Contract / Item"
              className="border border-neutral-300 rounded p-1.5"
              required
            />
            <input
              type="text"
              name="vendor"
              placeholder="Vendor / Landlord"
              className="border border-neutral-300 rounded p-1.5"
            />
            <input
              type="date"
              name="expiry"
              className="border border-neutral-300 rounded p-1.5"
            />
            <button
              type="submit"
              className="px-3 py-1.5 bg-neutral-900 text-white rounded font-medium hover:bg-neutral-800"
            >
              <Plus className="w-3.5 h-3.5 inline mr-1" /> Add
            </button>
          </form>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="bg-neutral-50 text-neutral-500">
              <tr>
                <th className="px-6 py-3 font-medium">Record</th>
                <th className="px-6 py-3 font-medium">Type</th>
                <th className="px-6 py-3 font-medium">Vendor / Contact</th>
                <th className="px-6 py-3 font-medium">Cost / Amount</th>
                <th className="px-6 py-3 font-medium">Expiration / Renewal</th>
                <th className="px-6 py-3 font-medium">Notice Window</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-200">
              {contracts.map((c: HouseContractRecord) => {
                const exp = c.expirationOrRenewalDate ? new Date(c.expirationOrRenewalDate) : null;
                const isExpiringSoon = exp && (exp.getTime() - now.getTime()) / (1000 * 60 * 60 * 24) < (c.noticePeriodDays || 30);

                return (
                  <tr key={c.id} className="hover:bg-neutral-50">
                    <td className="px-6 py-4 font-semibold text-neutral-900">{c.title}</td>
                    <td className="px-6 py-4">
                      <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium uppercase bg-amber-50 text-amber-800">
                        {c.recordType}
                      </span>
                    </td>
                    <td className="px-6 py-4 text-neutral-600">{c.vendorOrLandlord || "—"}</td>
                    <td className="px-6 py-4 font-mono text-neutral-800">
                      {c.amount > 0 ? `${c.amount.toFixed(2)} ${c.currency}` : "—"}
                    </td>
                    <td className="px-6 py-4">
                      {exp ? (
                        <div className="flex items-center space-x-1.5">
                          {isExpiringSoon && <AlertTriangle className="w-4 h-4 text-amber-500" />}
                          <span className={isExpiringSoon ? "text-amber-700 font-semibold" : "text-neutral-600"}>
                            {exp.toLocaleDateString()}
                          </span>
                        </div>
                      ) : (
                        <span className="text-neutral-400">Ongoing / No expiry</span>
                      )}
                    </td>
                    <td className="px-6 py-4 text-xs text-neutral-500">
                      {c.noticePeriodDays > 0 ? `${c.noticePeriodDays} days advance notice` : "Standard"}
                    </td>
                  </tr>
                );
              })}
              {contracts.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-6 py-8 text-center text-neutral-500">
                    No house contracts or warranties stored yet. Send contract photos in chat or add above.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8">
        {/* Preventive Maintenance Checklist */}
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
          <div className="p-6 border-b border-neutral-200 flex items-center justify-between">
            <div className="flex items-center space-x-2">
              <Wrench className="w-5 h-5 text-indigo-600" />
              <h2 className="text-base font-semibold text-neutral-900">Preventive Maintenance</h2>
            </div>
            <span className="text-xs text-neutral-400">Scheduled intervals</span>
          </div>

          <div className="divide-y divide-neutral-200">
            {maintenance.map((m: HomeMaintenanceItem) => {
              const due = new Date(m.nextDueAt);
              const isOverdue = now > due;

              return (
                <div key={m.id} className="p-4 hover:bg-neutral-50 flex items-center justify-between">
                  <div>
                    <div className="flex items-center space-x-2">
                      <span className="font-semibold text-neutral-900 text-sm">{m.taskName}</span>
                      <span className="text-[10px] uppercase bg-neutral-100 text-neutral-600 px-1.5 py-0.5 rounded">
                        {m.category}
                      </span>
                    </div>
                    <div className="text-xs text-neutral-500 mt-1 flex items-center space-x-3">
                      <span>Every {m.intervalDays} days</span>
                      <span>•</span>
                      <span className={isOverdue ? "text-red-600 font-semibold" : ""}>
                        Due: {due.toLocaleDateString()}
                      </span>
                      {m.contractorInfo && (
                        <>
                          <span>•</span>
                          <span className="text-neutral-400 truncate max-w-[120px]">{m.contractorInfo}</span>
                        </>
                      )}
                    </div>
                  </div>

                  <form action={completeTask}>
                    <input type="hidden" name="id" value={m.id} />
                    <button
                      type="submit"
                      className="px-3 py-1.5 bg-neutral-100 hover:bg-green-100 hover:text-green-800 text-neutral-700 rounded text-xs font-medium flex items-center transition-colors"
                    >
                      <CheckCircle className="w-3.5 h-3.5 mr-1" /> Complete
                    </button>
                  </form>
                </div>
              );
            })}
            {maintenance.length === 0 && (
              <div className="p-8 text-center text-xs text-neutral-500">
                AC filters, boiler checks, and seasonal upkeep schedules will be listed here.
              </div>
            )}
          </div>
        </div>

        {/* Vehicle Assets Vault */}
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
          <div className="p-6 border-b border-neutral-200 flex items-center justify-between">
            <div className="flex items-center space-x-2">
              <Car className="w-5 h-5 text-emerald-600" />
              <h2 className="text-base font-semibold text-neutral-900">Vehicle Assets</h2>
            </div>
            <span className="text-xs text-neutral-400">Licensing & service intervals</span>
          </div>

          <div className="p-6 space-y-6">
            {vehicles.map((v: VehicleAsset) => {
              const testExp = v.annualTestExpiry ? new Date(v.annualTestExpiry) : null;
              const insExp = v.insuranceExpiry ? new Date(v.insuranceExpiry) : null;
              const serviceDue = v.nextServiceMileageKm > 0 && v.currentMileageKm >= v.nextServiceMileageKm;

              return (
                <div key={v.id} className="bg-neutral-50 rounded-xl p-5 border border-neutral-200">
                  <div className="flex items-start justify-between mb-4">
                    <div>
                      <h3 className="text-base font-bold text-neutral-900">{v.name}</h3>
                      <span className="font-mono text-xs bg-white border border-neutral-200 px-2 py-0.5 rounded font-bold text-neutral-700">
                        {v.licensePlate}
                      </span>
                    </div>
                    {serviceDue && (
                      <span className="px-2 py-0.5 bg-red-100 text-red-800 rounded text-xs font-semibold">
                        Service Due
                      </span>
                    )}
                  </div>

                  <div className="grid grid-cols-2 gap-4 text-xs">
                    <div>
                      <span className="text-neutral-500">Current Mileage:</span>
                      <div className="font-bold text-neutral-800 text-sm mt-0.5">
                        {v.currentMileageKm.toLocaleString()} km
                      </div>
                    </div>
                    <div>
                      <span className="text-neutral-500">Next Service At:</span>
                      <div className="font-bold text-neutral-800 text-sm mt-0.5">
                        {v.nextServiceMileageKm > 0 ? `${v.nextServiceMileageKm.toLocaleString()} km` : "N/A"}
                      </div>
                    </div>
                    <div>
                      <span className="text-neutral-500">Annual Licensing Test:</span>
                      <div className="font-medium text-neutral-800 mt-0.5">
                        {testExp ? testExp.toLocaleDateString() : "—"}
                      </div>
                    </div>
                    <div>
                      <span className="text-neutral-500">Insurance Policy Expiry:</span>
                      <div className="font-medium text-neutral-800 mt-0.5">
                        {insExp ? insExp.toLocaleDateString() : "—"}
                      </div>
                    </div>
                  </div>
                </div>
              );
            })}
            {vehicles.length === 0 && (
              <div className="text-center text-xs text-neutral-500 py-6">
                No vehicles registered. Add vehicle details in chat (*&quot;Add our Tesla Model 3 plate 123-45-678&quot;*).
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
