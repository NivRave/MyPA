import { graphqlFetch, type AuditSession } from "@/lib/graphql";

export const dynamic = "force-dynamic";

const GET_AUDIT_SESSIONS = `
  query GetAuditSessions($limit: Int) {
    auditSessions(limit: $limit) {
      id
      userId
      source
      userPrompt
      status
      totalDurationMs
      startTime
    }
  }
`;

async function getAuditSessions(): Promise<AuditSession[]> {
  try {
    const data = await graphqlFetch<{ auditSessions: AuditSession[] }>(
      GET_AUDIT_SESSIONS,
      { limit: 50 }
    );
    return data.auditSessions || [];
  } catch (err) {
    console.error("Failed to fetch audit sessions:", err);
    return [];
  }
}

export default async function LogsPage() {
  const sessions = await getAuditSessions();

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      <div className="flex items-center justify-between mb-8">
        <h1 className="text-2xl font-semibold text-neutral-900">Audit Logs (Sessions)</h1>
      </div>

      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
        <table className="min-w-full divide-y divide-neutral-200">
          <thead className="bg-neutral-50">
            <tr>
              <th className="px-6 py-3 text-left text-xs font-medium text-neutral-500 uppercase tracking-wider">Timestamp</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-neutral-500 uppercase tracking-wider">User / Source</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-neutral-500 uppercase tracking-wider">Prompt</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-neutral-500 uppercase tracking-wider">Status</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-neutral-500 uppercase tracking-wider">Duration</th>
            </tr>
          </thead>
          <tbody className="bg-white divide-y divide-neutral-200">
            {sessions.map((session: AuditSession) => (
              <tr key={session.id} className="hover:bg-neutral-50">
                <td className="px-6 py-4 whitespace-nowrap text-sm text-neutral-900">
                  {new Date(session.startTime).toLocaleString()}
                </td>
                <td className="px-6 py-4 whitespace-nowrap text-sm text-neutral-500">
                  <span className="font-medium">{session.userId}</span>
                  <br />
                  <span className="text-xs">{session.source}</span>
                </td>
                <td className="px-6 py-4 text-sm text-neutral-900">
                  <div className="max-w-md truncate" title={session.userPrompt}>
                    {session.userPrompt || "—"}
                  </div>
                </td>
                <td className="px-6 py-4 whitespace-nowrap text-sm">
                  <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
                    session.status === "success" ? "bg-green-100 text-green-800" : "bg-red-100 text-red-800"
                  }`}>
                    {session.status || "unknown"}
                  </span>
                </td>
                <td className="px-6 py-4 whitespace-nowrap text-sm text-neutral-500">
                  {session.totalDurationMs} ms
                </td>
              </tr>
            ))}
            {sessions.length === 0 && (
              <tr>
                <td colSpan={5} className="px-6 py-8 text-center text-sm text-neutral-500">
                  No audit logs found.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
