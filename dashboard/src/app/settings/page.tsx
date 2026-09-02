export default function SettingsPage() {
  return (
    <div className="flex-1 p-8 overflow-y-auto">
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-semibold text-neutral-900">Settings</h1>
          <p className="text-sm text-neutral-500 mt-1">Configure global platform preferences.</p>
        </div>
      </div>

      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 divide-y divide-neutral-200 max-w-4xl">
        <div className="p-6 flex items-center justify-between">
          <div>
            <h3 className="font-medium text-neutral-900">LLM Provider</h3>
            <p className="text-sm text-neutral-500">Select the primary model used for orchestrator reasoning.</p>
          </div>
          <select className="border border-neutral-300 rounded-md text-sm py-1.5 px-3 bg-white text-neutral-900 font-medium">
            <option>Gemini 2.5 Flash</option>
            <option>Gemini 2.5 Pro</option>
            <option>Local (Ollama)</option>
          </select>
        </div>
        
        <div className="p-6 flex items-center justify-between">
          <div>
            <h3 className="font-medium text-neutral-900">Strict Pairing Codes</h3>
            <p className="text-sm text-neutral-500">Require an admin-generated pairing code for all new users.</p>
          </div>
          <div className="relative inline-block w-10 mr-2 align-middle select-none transition duration-200 ease-in">
            <input type="checkbox" name="toggle" id="toggle1" className="toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 appearance-none cursor-pointer" defaultChecked />
            <label htmlFor="toggle1" className="toggle-label block overflow-hidden h-5 rounded-full bg-indigo-500 cursor-pointer"></label>
          </div>
        </div>
        
        <div className="p-6 flex items-center justify-between">
          <div>
            <h3 className="font-medium text-neutral-900">Local Companion Node</h3>
            <p className="text-sm text-neutral-500">Allow execution of scripts via connected desktop daemons.</p>
          </div>
          <div className="relative inline-block w-10 mr-2 align-middle select-none transition duration-200 ease-in">
            <input type="checkbox" name="toggle" id="toggle2" className="toggle-checkbox absolute block w-5 h-5 rounded-full bg-white border-4 appearance-none cursor-pointer" />
            <label htmlFor="toggle2" className="toggle-label block overflow-hidden h-5 rounded-full bg-neutral-300 cursor-pointer"></label>
          </div>
        </div>
      </div>
    </div>
  );
}
