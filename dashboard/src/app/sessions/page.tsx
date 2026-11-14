import { graphqlFetch, type UserSessionSummary } from "@/lib/graphql";

export const dynamic = "force-dynamic";

const GET_USER_SESSIONS = `
  query GetUserSessions {
    userSessions {
      userId
      source
      totalInteractions
      lastActive
    }
  }
`;

async function getUserSessions(): Promise<UserSessionSummary[]> {
  try {
    const data = await graphqlFetch<{ userSessions: UserSessionSummary[] }>(
      GET_USER_SESSIONS
    );
    return data.userSessions || [];
  } catch (err) {
    console.error("Failed to fetch user sessions:", err);
    return [];
  }
}

export default async function SessionsPage() {
  const sessions = await getUserSessions();

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      <div className="flex items-center justify-between mb-8">
        <h1 className="text-2xl font-semibold text-neutral-900">User Sessions</h1>
      </div>

      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
        <table className="min-w-full divide-y divide-neutral-200">
          <thead className="bg-neutral-50">
            <tr>
              <th className="px-6 py-3 text-left text-xs font-medium text-neutral-500 uppercase tracking-wider">User ID</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-neutral-500 uppercase tracking-wider">Platform</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-neutral-500 uppercase tracking-wider">Total Interactions</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-neutral-500 uppercase tracking-wider">Last Active</th>
              <th className="px-6 py-3 text-right text-xs font-medium text-neutral-500 uppercase tracking-wider">Actions</th>
            </tr>
          </thead>
          <tbody className="bg-white divide-y divide-neutral-200">
            {sessions.map((session: UserSessionSummary) => (
              <tr key={session.userId} className="hover:bg-neutral-50">
                <td className="px-6 py-4 whitespace-nowrap text-sm font-medium text-neutral-900">
                  {session.userId}
                </td>
                <td className="px-6 py-4 whitespace-nowrap text-sm text-neutral-500 capitalize">
                  {session.source || "unknown"}
                </td>
                <td className="px-6 py-4 whitespace-nowrap text-sm text-neutral-500">
                  {session.totalInteractions}
                </td>
                <td className="px-6 py-4 whitespace-nowrap text-sm text-neutral-500">
                  {new Date(session.lastActive).toLocaleString()}
                </td>
                <td className="px-6 py-4 whitespace-nowrap text-right text-sm font-medium">
                  <button className="text-indigo-600 hover:text-indigo-900 mr-4">View History</button>
                  <button className="text-red-600 hover:text-red-900">Revoke</button>
                </td>
              </tr>
            ))}
            {sessions.length === 0 && (
              <tr>
                <td colSpan={5} className="px-6 py-8 text-center text-sm text-neutral-500">
                  No active sessions found.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
