import { revalidatePath } from "next/cache";
import { DollarSign, PieChart, RefreshCw, AlertCircle, Plus, ArrowUpRight, ArrowDownLeft } from "lucide-react";
import {
  graphqlFetch,
  type FinancialTransaction,
  type CategoryBudget,
  type RecurringBill,
} from "@/lib/graphql";

export const dynamic = "force-dynamic";

const GET_FINANCE_DATA_QUERY = `
  query GetFinanceData($familyGroup: String!) {
    financialTransactions(familyGroup: $familyGroup, limit: 50) {
      id
      type
      category
      merchant
      amount
      currency
      transactionDate
      isShared
      source
      notes
    }
    categoryBudgets(familyGroup: $familyGroup) {
      id
      category
      monthlyLimit
      currency
      alertThresholdPct
    }
    recurringBills(familyGroup: $familyGroup) {
      id
      name
      category
      expectedAmount
      currency
      billingCycle
      nextDueDay
      autoRenew
    }
  }
`;

const LOG_TRANSACTION_MUTATION = `
  mutation LogTx($familyGroup: String!, $type: String!, $category: String!, $merchant: String!, $amount: Float!, $currency: String!, $notes: String) {
    logFinancialTransaction(familyGroup: $familyGroup, type: $type, category: $category, merchant: $merchant, amount: $amount, currency: $currency, notes: $notes) { id }
  }
`;

const SET_BUDGET_MUTATION = `
  mutation SetBudget($familyGroup: String!, $category: String!, $monthlyLimit: Float!, $currency: String!) {
    setCategoryBudget(familyGroup: $familyGroup, category: $category, monthlyLimit: $monthlyLimit, currency: $currency) { id }
  }
`;

async function getFinanceData(familyGroup: string = "Smith Family"): Promise<{
  transactions: FinancialTransaction[];
  budgets: CategoryBudget[];
  bills: RecurringBill[];
}> {
  try {
    const data = await graphqlFetch<{
      financialTransactions: FinancialTransaction[];
      categoryBudgets: CategoryBudget[];
      recurringBills: RecurringBill[];
    }>(GET_FINANCE_DATA_QUERY, { familyGroup });

    return {
      transactions: data.financialTransactions || [],
      budgets: data.categoryBudgets || [],
      bills: data.recurringBills || [],
    };
  } catch (err) {
    console.error("Failed to load finance data:", err);
    return { transactions: [], budgets: [], bills: [] };
  }
}

export default async function FinancePage() {
  const familyGroup = "Smith Family";
  const { transactions, budgets, bills } = await getFinanceData(familyGroup);

  // Compute spend totals by category
  const spendByCategory: Record<string, number> = {};
  let totalExpense = 0;
  let totalIncome = 0;

  for (const t of transactions) {
    if (t.type === "expense") {
      spendByCategory[t.category] = (spendByCategory[t.category] || 0) + t.amount;
      totalExpense += t.amount;
    } else {
      totalIncome += t.amount;
    }
  }

  async function addTransaction(formData: FormData) {
    "use server";
    const typeArg = (formData.get("type") as string) || "expense";
    const category = (formData.get("category") as string) || "other";
    const merchant = (formData.get("merchant") as string) || "";
    const amount = parseFloat((formData.get("amount") as string) || "0") || 0;
    const currency = (formData.get("currency") as string) || "USD";
    const notes = (formData.get("notes") as string) || "";

    try {
      await graphqlFetch(LOG_TRANSACTION_MUTATION, {
        familyGroup: "Smith Family",
        type: typeArg,
        category,
        merchant,
        amount,
        currency,
        notes,
      });
    } catch (err) {
      console.error("addTransaction failed:", err);
    }
    revalidatePath("/finance");
  }

  async function saveBudget(formData: FormData) {
    "use server";
    const category = (formData.get("category") as string) || "other";
    const monthlyLimit = parseFloat((formData.get("limit") as string) || "0") || 0;
    const currency = (formData.get("currency") as string) || "USD";

    try {
      await graphqlFetch(SET_BUDGET_MUTATION, {
        familyGroup: "Smith Family",
        category,
        monthlyLimit,
        currency,
      });
    } catch (err) {
      console.error("saveBudget failed:", err);
    }
    revalidatePath("/finance");
  }

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-semibold text-neutral-900 flex items-center">
            <DollarSign className="mr-3 h-7 w-7 text-emerald-600" />
            Financial Tracking & Budget Intelligence
          </h1>
          <p className="text-sm text-neutral-500 mt-1">
            Real-time expense ledger, multi-channel receipt ingestion, monthly budget limits, and subscription tracking.
          </p>
        </div>
      </div>

      {/* Overview Cards */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-6 mb-8">
        <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm flex items-center justify-between">
          <div>
            <span className="text-xs font-semibold text-neutral-500 uppercase tracking-wider">Total Expenses</span>
            <div className="text-2xl font-bold text-neutral-900 mt-1">
              ${totalExpense.toFixed(2)}
            </div>
            <span className="text-xs text-neutral-400">Current cycle</span>
          </div>
          <div className="p-3 bg-red-50 text-red-600 rounded-lg">
            <ArrowUpRight className="w-6 h-6" />
          </div>
        </div>

        <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm flex items-center justify-between">
          <div>
            <span className="text-xs font-semibold text-neutral-500 uppercase tracking-wider">Total Income</span>
            <div className="text-2xl font-bold text-neutral-900 mt-1">
              ${totalIncome.toFixed(2)}
            </div>
            <span className="text-xs text-neutral-400">Current cycle</span>
          </div>
          <div className="p-3 bg-emerald-50 text-emerald-600 rounded-lg">
            <ArrowDownLeft className="w-6 h-6" />
          </div>
        </div>

        <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm flex items-center justify-between">
          <div>
            <span className="text-xs font-semibold text-neutral-500 uppercase tracking-wider">Active Budgets</span>
            <div className="text-2xl font-bold text-neutral-900 mt-1">
              {budgets.length} Categories
            </div>
            <span className="text-xs text-neutral-400">Threshold alerts configured</span>
          </div>
          <div className="p-3 bg-blue-50 text-blue-600 rounded-lg">
            <PieChart className="w-6 h-6" />
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-8 mb-8">
        {/* Monthly Budget Progress Bars */}
        <div className="lg:col-span-2 bg-white rounded-xl shadow-sm border border-neutral-200 p-6">
          <div className="flex items-center justify-between mb-6">
            <div className="flex items-center space-x-2">
              <PieChart className="w-5 h-5 text-indigo-600" />
              <h2 className="text-base font-semibold text-neutral-900">Category Budgets & Progress</h2>
            </div>
          </div>

          <div className="space-y-5">
            {budgets.map((b: CategoryBudget) => {
              const currentSpend = spendByCategory[b.category] || 0;
              const pct = b.monthlyLimit > 0 ? (currentSpend / b.monthlyLimit) * 100 : 0;
              const isOver = pct >= 100;
              const isNear = pct >= (b.alertThresholdPct || 80);

              return (
                <div key={b.id} className="space-y-1.5">
                  <div className="flex justify-between text-xs">
                    <span className="font-semibold text-neutral-800 capitalize">{b.category}</span>
                    <span className="text-neutral-500 font-mono">
                      ${currentSpend.toFixed(2)} / ${b.monthlyLimit.toFixed(2)} ({pct.toFixed(0)}%)
                    </span>
                  </div>
                  <div className="w-full bg-neutral-100 rounded-full h-2.5 overflow-hidden">
                    <div
                      className={`h-2.5 rounded-full transition-all ${
                        isOver ? "bg-red-500" : isNear ? "bg-amber-500" : "bg-emerald-500"
                      }`}
                      style={{ width: `${Math.min(pct, 100)}%` }}
                    />
                  </div>
                  {isOver && (
                    <span className="text-[11px] text-red-600 font-medium flex items-center">
                      <AlertCircle className="w-3.5 h-3.5 mr-1" /> Budget exceeded!
                    </span>
                  )}
                </div>
              );
            })}
            {budgets.length === 0 && (
              <div className="text-center text-xs text-neutral-400 py-6">
                No budgets set. Configure monthly limits for Baby, House, Groceries, or Dining below.
              </div>
            )}
          </div>

          <div className="mt-8 pt-6 border-t border-neutral-100">
            <h3 className="text-xs font-semibold text-neutral-700 uppercase mb-3">Set / Update Budget</h3>
            <form action={saveBudget} className="flex flex-wrap gap-3 text-xs">
              <select name="category" className="border border-neutral-300 rounded p-1.5 bg-white">
                <option value="groceries">Groceries</option>
                <option value="baby">Baby Care</option>
                <option value="house">House & Maintenance</option>
                <option value="utilities">Utilities</option>
                <option value="dining">Dining & Takeout</option>
                <option value="health">Health & Medical</option>
                <option value="other">Other</option>
              </select>
              <input
                type="number"
                step="any"
                name="limit"
                placeholder="Monthly Limit ($)"
                className="border border-neutral-300 rounded p-1.5 w-32"
                required
              />
              <button
                type="submit"
                className="px-3 py-1.5 bg-neutral-900 text-white rounded font-medium hover:bg-neutral-800"
              >
                Set Limit
              </button>
            </form>
          </div>
        </div>

        {/* Quick Log Transaction */}
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6 flex flex-col justify-between">
          <div>
            <h2 className="text-base font-semibold text-neutral-900 mb-4 flex items-center">
              <Plus className="w-5 h-5 mr-2 text-emerald-600" />
              Quick Log Transaction
            </h2>
            <form action={addTransaction} className="space-y-4 text-xs">
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="block text-neutral-600 mb-1">Type</label>
                  <select name="type" className="w-full border border-neutral-300 rounded p-2 bg-white">
                    <option value="expense">Expense</option>
                    <option value="income">Income</option>
                  </select>
                </div>
                <div>
                  <label className="block text-neutral-600 mb-1">Category</label>
                  <select name="category" className="w-full border border-neutral-300 rounded p-2 bg-white">
                    <option value="groceries">Groceries</option>
                    <option value="baby">Baby</option>
                    <option value="house">House</option>
                    <option value="utilities">Utilities</option>
                    <option value="dining">Dining</option>
                    <option value="health">Health</option>
                    <option value="other">Other</option>
                  </select>
                </div>
              </div>
              <div>
                <label className="block text-neutral-600 mb-1">Merchant / Source</label>
                <input
                  type="text"
                  name="merchant"
                  placeholder="e.g. Supermarket, Pharmacy"
                  className="w-full border border-neutral-300 rounded p-2"
                  required
                />
              </div>
              <div>
                <label className="block text-neutral-600 mb-1">Amount</label>
                <input
                  type="number"
                  step="any"
                  name="amount"
                  placeholder="0.00"
                  className="w-full border border-neutral-300 rounded p-2 text-sm font-bold"
                  required
                />
              </div>
              <div>
                <label className="block text-neutral-600 mb-1">Notes</label>
                <input
                  type="text"
                  name="notes"
                  placeholder="Optional details"
                  className="w-full border border-neutral-300 rounded p-2"
                />
              </div>
              <button
                type="submit"
                className="w-full py-2 bg-emerald-600 text-white rounded text-sm font-medium hover:bg-emerald-700 transition-colors"
              >
                Save Transaction
              </button>
            </form>
          </div>
        </div>
      </div>

      {/* Recurring Bills & Subscriptions */}
      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden mb-8">
        <div className="p-6 border-b border-neutral-200 flex items-center justify-between">
          <div className="flex items-center space-x-2">
            <RefreshCw className="w-5 h-5 text-indigo-600" />
            <h2 className="text-lg font-medium text-neutral-900">Recurring Bills & Subscriptions</h2>
          </div>
          <span className="text-xs text-neutral-400">Due day alerts & anomaly warnings</span>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="bg-neutral-50 text-neutral-500">
              <tr>
                <th className="px-6 py-3 font-medium">Subscription / Bill</th>
                <th className="px-6 py-3 font-medium">Category</th>
                <th className="px-6 py-3 font-medium">Expected Amount</th>
                <th className="px-6 py-3 font-medium">Cycle</th>
                <th className="px-6 py-3 font-medium">Next Due</th>
                <th className="px-6 py-3 font-medium">Auto Renew</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-200">
              {bills.map((b: RecurringBill) => (
                <tr key={b.id} className="hover:bg-neutral-50">
                  <td className="px-6 py-4 font-semibold text-neutral-900">{b.name}</td>
                  <td className="px-6 py-4 capitalize text-neutral-600">{b.category}</td>
                  <td className="px-6 py-4 font-mono font-bold text-neutral-900">
                    ${b.expectedAmount.toFixed(2)}
                  </td>
                  <td className="px-6 py-4 capitalize text-xs text-neutral-500">{b.billingCycle}</td>
                  <td className="px-6 py-4 text-xs text-neutral-700 font-medium">
                    Day {b.nextDueDay} of month
                  </td>
                  <td className="px-6 py-4">
                    <span className={`px-2 py-0.5 rounded text-xs font-medium ${b.autoRenew ? "bg-green-100 text-green-800" : "bg-neutral-100 text-neutral-600"}`}>
                      {b.autoRenew ? "Auto" : "Manual"}
                    </span>
                  </td>
                </tr>
              ))}
              {bills.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-6 py-8 text-center text-neutral-500">
                    No recurring bills tracked. Ask MyPA in chat to track electricity, rent, or Netflix subscriptions.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Transactions Ledger */}
      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
        <div className="p-6 border-b border-neutral-200">
          <h2 className="text-lg font-medium text-neutral-900">Transactions Ledger</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="bg-neutral-50 text-neutral-500">
              <tr>
                <th className="px-6 py-3 font-medium">Date</th>
                <th className="px-6 py-3 font-medium">Merchant</th>
                <th className="px-6 py-3 font-medium">Category</th>
                <th className="px-6 py-3 font-medium">Source</th>
                <th className="px-6 py-3 font-medium">Notes</th>
                <th className="px-6 py-3 font-medium text-right">Amount</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-200">
              {transactions.map((t: FinancialTransaction) => {
                const isExpense = t.type === "expense";
                return (
                  <tr key={t.id} className="hover:bg-neutral-50">
                    <td className="px-6 py-4 whitespace-nowrap text-xs text-neutral-500">
                      {new Date(t.transactionDate).toLocaleDateString()}
                    </td>
                    <td className="px-6 py-4 font-semibold text-neutral-900">{t.merchant}</td>
                    <td className="px-6 py-4 capitalize text-xs text-neutral-600">{t.category}</td>
                    <td className="px-6 py-4">
                      <span className="inline-flex items-center text-[10px] uppercase font-mono px-2 py-0.5 rounded bg-neutral-100 text-neutral-600">
                        {t.source}
                      </span>
                    </td>
                    <td className="px-6 py-4 text-xs text-neutral-500 max-w-xs truncate">{t.notes || "—"}</td>
                    <td className={`px-6 py-4 text-right font-mono font-bold ${isExpense ? "text-red-600" : "text-emerald-600"}`}>
                      {isExpense ? "-" : "+"}${t.amount.toFixed(2)}
                    </td>
                  </tr>
                );
              })}
              {transactions.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-6 py-8 text-center text-neutral-500">
                    No transactions recorded. Log expenses via chat, Telegram receipt photos, or the form above.
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
