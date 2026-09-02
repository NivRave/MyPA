import { ShieldCheck, Plus, RefreshCw, Trash2 } from "lucide-react";

export default function OAuthPage() {
  // Mock data for OAuth connections
  const connections = [
    {
      id: "google",
      provider: "Google",
      status: "connected",
      connectedAs: "admin@mypa.local",
      scopes: ["Calendar (Read/Write)", "Contacts (Read)"],
      lastSync: "10 minutes ago",
    },
    {
      id: "spotify",
      provider: "Spotify",
      status: "disconnected",
      connectedAs: null,
      scopes: [],
      lastSync: null,
    }
  ];

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-semibold text-neutral-900">OAuth Connections</h1>
          <p className="text-sm text-neutral-500 mt-1">Manage third-party authentication and API access.</p>
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        {connections.map((conn) => (
          <div key={conn.id} className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6">
            <div className="flex items-start justify-between mb-6">
              <div className="flex items-center space-x-3">
                <div className={`p-3 rounded-xl ${conn.status === 'connected' ? 'bg-green-50 text-green-600' : 'bg-neutral-100 text-neutral-400'}`}>
                  <ShieldCheck className="w-6 h-6" />
                </div>
                <div>
                  <h3 className="font-semibold text-neutral-900 text-lg">{conn.provider}</h3>
                  <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium mt-1 ${
                    conn.status === 'connected' ? 'bg-green-100 text-green-800' : 'bg-neutral-100 text-neutral-600'
                  }`}>
                    {conn.status === 'connected' ? 'Connected' : 'Not Connected'}
                  </span>
                </div>
              </div>
            </div>
            
            {conn.status === 'connected' ? (
              <div className="space-y-4">
                <div>
                  <p className="text-xs text-neutral-500 uppercase font-semibold tracking-wider mb-1">Connected Account</p>
                  <p className="text-sm font-medium text-neutral-900">{conn.connectedAs}</p>
                </div>
                <div>
                  <p className="text-xs text-neutral-500 uppercase font-semibold tracking-wider mb-1">Granted Scopes</p>
                  <ul className="text-sm text-neutral-700 list-disc list-inside">
                    {conn.scopes.map((scope, i) => <li key={i}>{scope}</li>)}
                  </ul>
                </div>
                <div className="pt-4 border-t border-neutral-100 flex items-center justify-between mt-4">
                  <span className="text-xs text-neutral-400 flex items-center">
                    <RefreshCw className="w-3 h-3 mr-1" />
                    Last synced {conn.lastSync}
                  </span>
                  <button className="flex items-center text-sm font-medium text-red-600 hover:text-red-700 transition-colors">
                    <Trash2 className="w-4 h-4 mr-1" />
                    Disconnect
                  </button>
                </div>
              </div>
            ) : (
              <div className="flex flex-col items-center justify-center py-6 text-center">
                <p className="text-sm text-neutral-500 mb-4">Connect your {conn.provider} account to enable related features and tools.</p>
                <button className="flex items-center px-4 py-2 bg-neutral-900 text-white text-sm font-medium rounded-md hover:bg-neutral-800 transition-colors">
                  <Plus className="w-4 h-4 mr-2" />
                  Connect {conn.provider}
                </button>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
