import type { JoinStep } from '@/lib/graphql';

// V4-41: what a new member does on a channel before their first message reaches MyPA, shown
// next to the pairing codes so the owner can pass it on with the code. The steps come from the
// channel adapters through the API; this component knows no channel. Text in backticks is
// something to type exactly, shown as code.
export function JoinSteps({ steps }: { steps: JoinStep[] }) {
  if (steps.length === 0) {
    return null;
  }
  return (
    <div className="bg-amber-50 rounded-xl border border-amber-200 p-6" data-testid="join-steps">
      <h2 className="text-lg font-medium text-neutral-900 mb-2">Before the code</h2>
      <p className="text-sm text-neutral-600 mb-3">Send these with the code: the first message only reaches MyPA after this step.</p>
      <ul className="space-y-2 text-sm text-neutral-800">
        {steps.map((s) => (
          <li key={s.channel}>
            <span className="font-medium capitalize">{s.channel}:</span> {withCode(s.step)}
          </li>
        ))}
      </ul>
    </div>
  );
}

// withCode renders `text in backticks` as code.
function withCode(text: string) {
  return text.split('`').map((part, i) =>
    i % 2 === 1 ? (
      <code key={i} className="font-mono bg-white border border-amber-200 rounded px-1">
        {part}
      </code>
    ) : (
      <span key={i}>{part}</span>
    ),
  );
}
