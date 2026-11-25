import type { MemberAccount } from '@/lib/graphql';

export interface MemberWithAccounts {
  id: string;
  name: string;
  accounts: MemberAccount[];
}

// BL-41: an owner sees each member's channel accounts (the channel and the end of the number only)
// and can remove one, for example an account a stolen phone added. A person's last account cannot
// be removed, so it has no button; the server refuses it as well. The person's other accounts and
// the owners are told in chat.
export function MemberAccounts({
  members,
  removeAction,
  error,
}: {
  members: MemberWithAccounts[];
  removeAction: (formData: FormData) => void | Promise<void>;
  error?: string;
}) {
  return (
    <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden" data-testid="member-accounts">
      <div className="p-6 border-b border-neutral-200">
        <h2 className="text-lg font-medium text-neutral-900">Channel accounts</h2>
        <p className="text-sm text-neutral-500 mt-1">
          Remove an account a member no longer has, or one they did not add. The last account stays. The member and the owners are told in chat.
        </p>
        {error && (
          <p role="alert" className="text-sm text-red-700 mt-2">
            {error}
          </p>
        )}
      </div>
      <ul className="divide-y divide-neutral-200">
        {members.map((m) => (
          <li key={m.id} className="p-6">
            <h3 className="font-medium text-neutral-900 mb-2">{m.name}</h3>
            <ul className="space-y-2">
              {m.accounts.map((a) => (
                <li key={a.id} className="flex items-center justify-between text-sm">
                  <span className="text-neutral-700">
                    {a.label}
                    <span className="text-neutral-400"> · added {new Date(a.linkedAt).toLocaleDateString()}</span>
                  </span>
                  {m.accounts.length > 1 ? (
                    <form action={removeAction}>
                      <input type="hidden" name="personId" value={m.id} />
                      <input type="hidden" name="accountId" value={a.id} />
                      <button type="submit" className="text-red-600 hover:text-red-800 font-medium">
                        Remove
                      </button>
                    </form>
                  ) : (
                    <span className="text-neutral-400">Last account</span>
                  )}
                </li>
              ))}
              {m.accounts.length === 0 && <li className="text-sm text-neutral-500">No accounts.</li>}
            </ul>
          </li>
        ))}
      </ul>
    </div>
  );
}
