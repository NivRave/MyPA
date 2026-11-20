import { revalidatePath } from "next/cache";
import { History, RotateCcw, CheckCircle, Clock, AlertTriangle, Filter } from "lucide-react";
import { getMe, graphqlFetch, type MutationLog } from "@/lib/graphql";
import { householdTopics, topicLabel } from "@/lib/packs";

export const dynamic = "force-dynamic";

const GET_MUTATION_LOGS = `
  query GetMutationLogs($topic: String) {
    mutationLogs(topic: $topic, limit: 100) {
      id
      userId
      familyGroup
      topic
      action
      entityType
      entityId
      beforeJson
      afterJson
      summary
      isUndone
      createdAt
    }
  }
`;

const ROLLBACK_MUTATION = `
  mutation Rollback($mutationId: ID!) {
    rollbackMutation(mutationId: $mutationId)
  }
`;

async function getMutationLogs(topic?: string): Promise<MutationLog[]> {
  try {
    const data = await graphqlFetch<{ mutationLogs: MutationLog[] }>(
      GET_MUTATION_LOGS,
      { topic: topic && topic !== "all" ? topic : null }
    );
    return data.mutationLogs || [];
  } catch (err) {
    console.error("Failed to fetch mutation logs:", err);
    return [];
  }
}

export default async function AuditRollbackPage({
  searchParams,
}: {
  searchParams?: Promise<{ topic?: string }>;
}) {
  const params = await searchParams;
  const currentTopic = params?.topic || "all";
  const logs = await getMutationLogs(currentTopic);

  // V4-27: the household's topics (Core plus its packs'), then schedule and memory.
  const me = await getMe();
  const topics = [
    { id: "all", label: "All Topics" },
    ...householdTopics(me).map((t) => ({ id: t, label: topicLabel(t) })),
    { id: "schedule", label: "Schedule" },
    { id: "memory", label: "Memory" },
  ];

  async function rollback(formData: FormData) {
    "use server";
    const mutationId = formData.get("mutationId") as string;
    try {
      await graphqlFetch(ROLLBACK_MUTATION, { mutationId });
    } catch (err) {
      console.error("Rollback failed:", err);
    }
    revalidatePath("/audit");
  }

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-semibold text-neutral-900 flex items-center">
            <History className="mr-3 h-7 w-7 text-red-500" />
            Mutation Audit Logs & Rollback Engine
          </h1>
          <p className="text-sm text-neutral-500 mt-1">
            Complete audit trail of AI and user mutations with snapshot states and one-click rollback capability.
          </p>
        </div>
      </div>

      {/* Filter Bar */}
      <div className="flex items-center space-x-2 mb-8 bg-white p-4 rounded-xl border border-neutral-200 shadow-sm">
        <Filter className="w-4 h-4 text-neutral-400 mr-2" />
        <span className="text-xs text-neutral-500 font-medium mr-2">Filter by Topic:</span>
        {topics.map((t) => (
          <a
            key={t.id}
            href={`/audit?topic=${t.id}`}
            className={`px-3 py-1.5 text-xs font-medium rounded-full transition-colors ${
              currentTopic === t.id
                ? "bg-neutral-900 text-white"
                : "bg-neutral-100 text-neutral-600 hover:bg-neutral-200"
            }`}
          >
            {t.label}
          </a>
        ))}
      </div>

      {/* Mutation Logs Table */}
      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
        <div className="p-6 border-b border-neutral-200 flex items-center justify-between">
          <h2 className="text-lg font-medium text-neutral-900">
            Audit Records ({logs.length})
          </h2>
          <span className="text-xs text-neutral-400">
            Immutable log of state-changing actions
          </span>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="bg-neutral-50 text-neutral-500">
              <tr>
                <th className="px-6 py-3 font-medium">Timestamp</th>
                <th className="px-6 py-3 font-medium">Topic / Domain</th>
                <th className="px-6 py-3 font-medium">Action</th>
                <th className="px-6 py-3 font-medium">Entity Type</th>
                <th className="px-6 py-3 font-medium">Summary</th>
                <th className="px-6 py-3 font-medium">Status</th>
                <th className="px-6 py-3 font-medium text-right">Rollback</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-200">
              {logs.map((log: MutationLog) => {
                return (
                  <tr key={log.id} className={`hover:bg-neutral-50 ${log.isUndone ? "opacity-60 bg-neutral-50/50" : ""}`}>
                    <td className="px-6 py-4 whitespace-nowrap text-xs text-neutral-500">
                      <span className="inline-flex items-center">
                        <Clock className="w-3.5 h-3.5 mr-1 text-neutral-400" />
                        {new Date(log.createdAt).toLocaleString()}
                      </span>
                    </td>
                    <td className="px-6 py-4">
                      <span className="text-xs font-semibold uppercase px-2 py-0.5 rounded bg-neutral-100 text-neutral-700">
                        {log.topic}
                      </span>
                    </td>
                    <td className="px-6 py-4">
                      <span className={`text-xs font-medium capitalize px-2 py-0.5 rounded ${
                        log.action === "create" ? "bg-green-100 text-green-800" :
                        log.action === "delete" ? "bg-red-100 text-red-800" : "bg-blue-100 text-blue-800"
                      }`}>
                        {log.action}
                      </span>
                    </td>
                    <td className="px-6 py-4 text-xs font-mono text-neutral-600">
                      {log.entityType} ({log.entityId || "—"})
                    </td>
                    <td className="px-6 py-4 text-neutral-800 max-w-md">
                      <div className="font-medium text-xs">{log.summary}</div>
                      {log.beforeJson && log.beforeJson !== "{}" && (
                        <details className="mt-1">
                          <summary className="text-[11px] text-neutral-400 cursor-pointer hover:text-neutral-600">
                            View Snapshot Data
                          </summary>
                          <pre className="text-[10px] bg-neutral-50 p-2 rounded mt-1 overflow-x-auto max-h-24">
                            {log.beforeJson}
                          </pre>
                        </details>
                      )}
                    </td>
                    <td className="px-6 py-4">
                      {log.isUndone ? (
                        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-neutral-200 text-neutral-600">
                          <AlertTriangle className="w-3 h-3 mr-1" /> Undone
                        </span>
                      ) : (
                        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-green-100 text-green-800">
                          <CheckCircle className="w-3 h-3 mr-1" /> Active
                        </span>
                      )}
                    </td>
                    <td className="px-6 py-4 text-right">
                      {!log.isUndone ? (
                        <form action={rollback}>
                          <input type="hidden" name="mutationId" value={log.id} />
                          <button
                            type="submit"
                            title="Undo this action and restore previous state"
                            className="inline-flex items-center px-3 py-1 bg-red-50 text-red-700 hover:bg-red-100 rounded text-xs font-medium transition-colors"
                          >
                            <RotateCcw className="w-3.5 h-3.5 mr-1" /> Rollback
                          </button>
                        </form>
                      ) : (
                        <span className="text-xs text-neutral-400">—</span>
                      )}
                    </td>
                  </tr>
                );
              })}
              {logs.length === 0 && (
                <tr>
                  <td colSpan={7} className="px-6 py-12 text-center text-neutral-500">
                    No mutation audit logs found. As changes are made via chat, automations, or web UI, audit records will appear here.
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
