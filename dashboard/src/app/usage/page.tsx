import { graphqlFetch, type Usage } from "@/lib/graphql";
import { againstCap, formatTokens } from "@/lib/usage";

export const dynamic = "force-dynamic";

const GET_USAGE = `
  query GetUsage {
    usage(days: 14) {
      timeZone
      personDailyCap
      installDailyCap
      myTokensToday
      installTokensToday
      days { day personId name inputTokens outputTokens calls turns }
      turns { turnId personId name startedAt inputTokens outputTokens calls providers }
    }
  }
`;

async function getUsage(): Promise<Usage | null> {
  try {
    const data = await graphqlFetch<{ usage: Usage }>(GET_USAGE);
    return data.usage ?? null;
  } catch (err) {
    console.error("Failed to fetch usage:", err);
    return null;
  }
}

const fmt = formatTokens;

function CapCard({ title, used, cap, testId }: { title: string; used: number; cap: number; testId: string }) {
  const share = cap > 0 ? Math.min(100, Math.round((used / cap) * 100)) : 0;
  const reached = cap > 0 && used >= cap;
  return (
    <div data-testid={testId} className="bg-white rounded-xl shadow-sm border border-neutral-200 p-5">
      <div className="text-sm font-medium text-neutral-500">{title}</div>
      <div className="mt-1 text-xl font-semibold text-neutral-900">{againstCap(used, cap)}</div>
      {cap > 0 && (
        <div className="mt-3 h-2 rounded-full bg-neutral-100 overflow-hidden" aria-hidden="true">
          <div className={`h-2 ${reached ? "bg-red-500" : "bg-indigo-500"}`} style={{ width: `${share}%` }} />
        </div>
      )}
      {reached && <div className="mt-2 text-sm text-red-600">Reached: new turns are refused until 00:00.</div>}
    </div>
  );
}

const th = "px-6 py-3 text-left text-xs font-medium text-neutral-500 uppercase tracking-wider";
const td = "px-6 py-4 whitespace-nowrap text-sm text-neutral-700";

/**
 * Chat model usage (V4-04): today's tokens against the daily caps, tokens per person and day, and
 * the latest turns. An owner sees each member, anyone else only themselves; the install's total is
 * shown to the operator only.
 */
export default async function UsagePage() {
  const usage = await getUsage();
  if (!usage) {
    return (
      <div className="flex-1 p-8">
        <h1 className="text-2xl font-semibold text-neutral-900 mb-4">Usage</h1>
        <p className="text-sm text-neutral-500">Usage could not be loaded.</p>
      </div>
    );
  }

  return (
    <div className="flex-1 p-8 overflow-y-auto space-y-8">
      <div>
        <h1 className="text-2xl font-semibold text-neutral-900">Usage</h1>
        <p className="mt-1 text-sm text-neutral-500">
          Chat model tokens. Days run from 00:00 in {usage.timeZone}, when the daily caps start again.
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <CapCard testId="my-cap" title="You today" used={usage.myTokensToday} cap={usage.personDailyCap} />
        {usage.installTokensToday !== null && (
          <CapCard testId="install-cap" title="The whole install today" used={usage.installTokensToday} cap={usage.installDailyCap} />
        )}
      </div>

      <section>
        <h2 className="text-lg font-semibold text-neutral-900 mb-3">By day</h2>
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-x-auto">
          <table className="min-w-full divide-y divide-neutral-200">
            <thead className="bg-neutral-50">
              <tr>
                <th className={th}>Day</th>
                <th className={th}>Person</th>
                <th className={th}>Input tokens</th>
                <th className={th}>Output tokens</th>
                <th className={th}>Model calls</th>
                <th className={th}>Turns</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-200">
              {usage.days.map((d) => (
                <tr key={`${d.day}-${d.personId}`}>
                  <td className={td}>{d.day}</td>
                  <td className={td}>{d.name || d.personId}</td>
                  <td className={td}>{fmt(d.inputTokens)}</td>
                  <td className={td}>{fmt(d.outputTokens)}</td>
                  <td className={td}>{d.calls}</td>
                  <td className={td}>{d.turns}</td>
                </tr>
              ))}
              {usage.days.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-6 py-8 text-center text-sm text-neutral-500">No usage in the last 14 days.</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section>
        <h2 className="text-lg font-semibold text-neutral-900 mb-3">Latest turns</h2>
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-x-auto">
          <table className="min-w-full divide-y divide-neutral-200">
            <thead className="bg-neutral-50">
              <tr>
                <th className={th}>Started</th>
                <th className={th}>Person</th>
                <th className={th}>Tokens (in / out)</th>
                <th className={th}>Model calls</th>
                <th className={th}>Answered by</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-200">
              {usage.turns.map((t) => (
                <tr key={t.turnId}>
                  <td className={td}>{new Date(t.startedAt).toLocaleString()}</td>
                  <td className={td}>{t.name || t.personId}</td>
                  <td className={td}>{fmt(t.inputTokens)} / {fmt(t.outputTokens)}</td>
                  <td className={td}>{t.calls}</td>
                  <td className={td}>{t.providers}</td>
                </tr>
              ))}
              {usage.turns.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-6 py-8 text-center text-sm text-neutral-500">No turns yet.</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
