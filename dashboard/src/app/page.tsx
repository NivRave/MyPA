export default function Home() {
  return (
    <>
      <header className="h-16 bg-white border-b border-neutral-200 flex items-center px-8 justify-between shrink-0">
        <h1 className="text-xl font-semibold text-neutral-800">Overview</h1>
      </header>
      <div className="p-8 overflow-y-auto">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6 mb-8">
          <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm">
            <h3 className="text-sm font-medium text-neutral-500">Active Sessions</h3>
            <p className="text-3xl font-bold text-neutral-900 mt-2">12</p>
          </div>
          <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm">
            <h3 className="text-sm font-medium text-neutral-500">Loaded Tools</h3>
            <p className="text-3xl font-bold text-neutral-900 mt-2">8</p>
          </div>
          <div className="bg-white p-6 rounded-xl border border-neutral-200 shadow-sm">
            <h3 className="text-sm font-medium text-neutral-500">Audit Events Today</h3>
            <p className="text-3xl font-bold text-neutral-900 mt-2">1,240</p>
          </div>
        </div>
        <div className="bg-white rounded-xl border border-neutral-200 shadow-sm p-6">
          <h2 className="text-lg font-medium text-neutral-900 mb-4">Welcome to MyPA V3</h2>
          <p className="text-neutral-600">
            The Web Control UI is now running. From here you can manage sessions, configure tools, and view audit logs. 
            Navigation is set up on the left to start building out the individual management screens.
          </p>
        </div>
      </div>
    </>
  );
}
