import { revalidatePath } from 'next/cache';
import { graphqlFetch, type User, type PairingCode } from '@/lib/graphql';

export const dynamic = "force-dynamic";

const GET_USERS_AND_CODES = `
  query GetUsersAndCodes {
    users {
      id
      platformId
      name
      role
      familyGroup
      createdAt
    }
    activePairingCodes {
      id
      code
      role
      familyGroup
      expiresAt
    }
  }
`;

const GENERATE_PAIRING_CODE_MUTATION = `
  mutation GenerateCode($role: String!, $familyGroup: String!) {
    generatePairingCode(role: $role, familyGroup: $familyGroup) {
      id
    }
  }
`;

export default async function UsersPage() {
  let data: { users: User[]; activePairingCodes: PairingCode[] } = {
    users: [],
    activePairingCodes: [],
  };

  try {
    data = await graphqlFetch<{ users: User[]; activePairingCodes: PairingCode[] }>(
      GET_USERS_AND_CODES
    );
  } catch (err) {
    console.error('Failed to fetch users and codes:', err);
  }

  async function createCode(formData: FormData) {
    'use server';
    const role = formData.get('role') as string;
    const familyGroup = formData.get('familyGroup') as string;

    try {
      await graphqlFetch(GENERATE_PAIRING_CODE_MUTATION, { role, familyGroup });
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
                  <th className="px-6 py-3 font-medium">Family Group</th>
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

        {/* Pairing Codes */}
        <div className="space-y-6">
          <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6">
            <h2 className="text-lg font-medium text-neutral-900 mb-4">Generate Pairing Code</h2>
            <form action={createCode} className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-neutral-700 mb-1">Role</label>
                <select name="role" className="w-full border border-neutral-300 rounded-md shadow-sm p-2 text-sm">
                  <option value="admin">Admin</option>
                  <option value="family">Family Member</option>
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-neutral-700 mb-1">Family Group</label>
                <input type="text" name="familyGroup" placeholder="e.g. Smith Family" className="w-full border border-neutral-300 rounded-md shadow-sm p-2 text-sm" />
              </div>
              <button type="submit" className="w-full bg-indigo-600 text-white rounded-md py-2 px-4 text-sm font-medium hover:bg-indigo-700 transition-colors">
                Generate Code
              </button>
            </form>
          </div>

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
      </div>
    </div>
  );
}
