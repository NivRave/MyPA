import { revalidatePath } from 'next/cache';
import { redirect } from 'next/navigation';
import { graphqlFetch, getMe, type User, type PairingCode, type JoinStep, type MemberAccount } from '@/lib/graphql';
import { JoinSteps } from '@/components/JoinSteps';
import { MemberAccounts, type MemberWithAccounts } from '@/components/MemberAccounts';

export const dynamic = "force-dynamic";

// V4-35: an owner sees the household's members with their roles and issues pairing codes; an
// adult sees only themselves. The household is the signed-in person's own, never an argument.
const GET_USERS = `
  query GetUsers {
    users {
      id
      platformId
      name
      role
      familyGroup
      createdAt
    }
  }
`;

const GET_CODES = `
  query GetCodes {
    activePairingCodes {
      id
      code
      role
      expiresAt
    }
    joinSteps {
      channel
      step
    }
  }
`;

const GENERATE_PAIRING_CODE_MUTATION = `
  mutation GenerateCode($role: String!) {
    generatePairingCode(role: $role) {
      id
    }
  }
`;

// BL-41: an owner lists each member's channel accounts and removes one; the last one stays.
const GET_MEMBER_ACCOUNTS = `
  query GetMemberAccounts($personId: ID!) {
    memberAccounts(personId: $personId) {
      id
      personId
      channel
      label
      linkedAt
    }
  }
`;

const REMOVE_MEMBER_ACCOUNT_MUTATION = `
  mutation RemoveMemberAccount($personId: ID!, $accountId: ID!) {
    removeMemberAccount(personId: $personId, accountId: $accountId)
  }
`;

// The link carries only a code for the refusal; the page shows its own text for it, never text
// from the link. A Map, so a code such as __proto__ finds nothing rather than an inherited value.
const REFUSALS = new Map<string, string>([
  ['last', "That is the person's last account, so it cannot be removed."],
  ['denied', "Only an owner of the household may remove a member's account."],
  ['missing', 'That account is not in your household any more.'],
  ['failed', 'The account could not be removed; nothing was changed.'],
]);

function refusalCode(message: string): string {
  if (message.includes('last account')) return 'last';
  if (message.includes('permission denied')) return 'denied';
  if (message.includes('no such account')) return 'missing';
  return 'failed';
}

export default async function UsersPage({
  searchParams,
}: {
  searchParams?: Promise<{ accountError?: string }>;
}) {
  const params = await searchParams;
  const me = await getMe();
  const isOwner = me?.role === 'owner';
  const data: { users: User[]; activePairingCodes: PairingCode[]; joinSteps: JoinStep[] } = {
    users: [],
    activePairingCodes: [],
    joinSteps: [],
  };

  try {
    data.users = (await graphqlFetch<{ users: User[] }>(GET_USERS)).users;
    if (isOwner) {
      const codes = await graphqlFetch<{ activePairingCodes: PairingCode[]; joinSteps: JoinStep[] }>(GET_CODES);
      data.activePairingCodes = codes.activePairingCodes;
      data.joinSteps = codes.joinSteps ?? [];
    }
  } catch (err) {
    console.error('Failed to fetch users and codes:', err);
  }

  const memberAccounts: MemberWithAccounts[] = [];
  if (isOwner) {
    for (const u of data.users) {
      try {
        const res = await graphqlFetch<{ memberAccounts: MemberAccount[] }>(GET_MEMBER_ACCOUNTS, { personId: u.id });
        memberAccounts.push({ id: u.id, name: u.name, accounts: res.memberAccounts });
      } catch (err) {
        console.error('Failed to fetch member accounts:', err);
      }
    }
  }

  async function removeAccount(formData: FormData) {
    'use server';
    const personId = formData.get('personId') as string;
    const accountId = formData.get('accountId') as string;
    let refusal = '';
    try {
      await graphqlFetch(REMOVE_MEMBER_ACCOUNT_MUTATION, { personId, accountId });
    } catch (err) {
      console.error('removeMemberAccount failed:', err);
      refusal = refusalCode(err instanceof Error ? err.message : '');
    }
    revalidatePath('/users');
    if (refusal) {
      redirect('/users?accountError=' + refusal);
    }
    redirect('/users');
  }

  async function createCode(formData: FormData) {
    'use server';
    const role = formData.get('role') as string;

    try {
      await graphqlFetch(GENERATE_PAIRING_CODE_MUTATION, { role });
    } catch (err) {
      console.error('generatePairingCode failed:', err);
    }

    revalidatePath('/users');
  }

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-semibold text-neutral-900">Users & Access</h1>
          <p className="text-sm text-neutral-500 mt-1">Manage user access and generate pairing codes.</p>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8">
        {/* Active Users */}
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
          <div className="p-6 border-b border-neutral-200">
            <h2 className="text-lg font-medium text-neutral-900">Active Users</h2>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-sm text-left">
              <thead className="bg-neutral-50 text-neutral-500">
                <tr>
                  <th className="px-6 py-3 font-medium">Platform ID</th>
                  <th className="px-6 py-3 font-medium">Name</th>
                  <th className="px-6 py-3 font-medium">Role</th>
                  <th className="px-6 py-3 font-medium">Household</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-neutral-200">
                {data.users.map((user: User) => (
                  <tr key={user.id} className="hover:bg-neutral-50">
                    <td className="px-6 py-4 text-neutral-900">{user.platformId}</td>
                    <td className="px-6 py-4 text-neutral-600">{user.name}</td>
                    <td className="px-6 py-4 text-neutral-600 capitalize">{user.role}</td>
                    <td className="px-6 py-4 text-neutral-600">{user.familyGroup || '-'}</td>
                  </tr>
                ))}
                {data.users.length === 0 && (
                  <tr>
                    <td colSpan={4} className="px-6 py-8 text-center text-neutral-500">No users found.</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>

        {/* Pairing Codes: an owner's */}
        {!isOwner ? (
          <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6 text-sm text-neutral-600">
            Only an owner of the household adds members and sees their roles. Ask an owner if someone should join.
          </div>
        ) : (
        <div className="space-y-6">
          <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6">
            <h2 className="text-lg font-medium text-neutral-900 mb-4">Generate Pairing Code</h2>
            <form action={createCode} className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-neutral-700 mb-1">Role</label>
                <select name="role" className="w-full border border-neutral-300 rounded-md shadow-sm p-2 text-sm">
                  <option value="adult">Adult</option>
                  <option value="child">Child (chat only)</option>
                </select>
                <p className="text-xs text-neutral-500 mt-1">A code never makes an owner. The new member joins your household.</p>
              </div>
              <button type="submit" className="w-full bg-indigo-600 text-white rounded-md py-2 px-4 text-sm font-medium hover:bg-indigo-700 transition-colors">
                Generate Code
              </button>
            </form>
          </div>

          <JoinSteps steps={data.joinSteps} />

          <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
            <div className="p-6 border-b border-neutral-200">
              <h2 className="text-lg font-medium text-neutral-900">Active Codes</h2>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-sm text-left">
                <thead className="bg-neutral-50 text-neutral-500">
                  <tr>
                    <th className="px-6 py-3 font-medium">Code</th>
                    <th className="px-6 py-3 font-medium">Role</th>
                    <th className="px-6 py-3 font-medium">Expires</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-neutral-200">
                  {data.activePairingCodes.map((code: PairingCode) => (
                    <tr key={code.id} className="hover:bg-neutral-50">
                      <td className="px-6 py-4 font-mono font-medium text-indigo-600">{code.code}</td>
                      <td className="px-6 py-4 text-neutral-600 capitalize">{code.role}</td>
                      <td className="px-6 py-4 text-neutral-500">{new Date(code.expiresAt).toLocaleString()}</td>
                    </tr>
                  ))}
                  {data.activePairingCodes.length === 0 && (
                    <tr>
                      <td colSpan={3} className="px-6 py-8 text-center text-neutral-500">No active codes.</td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </div>
        )}
      </div>

      {isOwner && memberAccounts.length > 0 && (
        <div className="mt-8">
          <MemberAccounts members={memberAccounts} removeAction={removeAccount} error={params?.accountError ? REFUSALS.get(params.accountError) : undefined} />
        </div>
      )}
    </div>
  );
}
