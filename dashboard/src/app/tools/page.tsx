import { Wrench, Plus, CheckCircle2, XCircle } from "lucide-react";

export default function ToolsPage() {
  // Mock data for tools/plugins until the backend MCP integration is complete
  const tools = [
    {
      id: "google_calendar",
      name: "Google Calendar",
      type: "Native Integration",
      status: "active",
      description: "Manage events and read calendar schedules.",
      version: "v1.0",
    },
    {
      id: "tavily_search",
      name: "Tavily Web Search",
      type: "Native Integration",
      status: "active",
      description: "Real-time web search for answering questions.",
      version: "v1.0",
    },
    {
      id: "mcp_home_assistant",
      name: "Home Assistant (MCP)",
      type: "MCP Server",
      status: "inactive",
      description: "Connects to local Home Assistant for smart home control.",
      version: "v0.9.1 (Beta)",
    },
    {
      id: "mcp_github",
      name: "GitHub Repository (MCP)",
      type: "MCP Server",
      status: "inactive",
      description: "Allows the assistant to read and write to GitHub repositories.",
      version: "v1.2",
    }
  ];

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-semibold text-neutral-900">Tools & Plugins</h1>
          <p className="text-sm text-neutral-500 mt-1">Manage native integrations and connected MCP (Model Context Protocol) servers.</p>
        </div>
        <button className="flex items-center px-4 py-2 bg-neutral-900 text-white text-sm font-medium rounded-md hover:bg-neutral-800 transition-colors">
          <Plus className="w-4 h-4 mr-2" />
          Add MCP Server
        </button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        {tools.map((tool) => (
          <div key={tool.id} className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6 flex flex-col">
            <div className="flex items-start justify-between mb-4">
              <div className="flex items-center space-x-3">
                <div className={`p-2 rounded-lg ${tool.status === 'active' ? 'bg-indigo-50 text-indigo-600' : 'bg-neutral-100 text-neutral-500'}`}>
                  <Wrench className="w-5 h-5" />
                </div>
                <div>
                  <h3 className="font-medium text-neutral-900">{tool.name}</h3>
                  <span className="text-xs text-neutral-500">{tool.type} • {tool.version}</span>
                </div>
              </div>
              {tool.status === 'active' ? (
                <CheckCircle2 className="w-5 h-5 text-green-500" />
              ) : (
                <XCircle className="w-5 h-5 text-neutral-300" />
              )}
            </div>
            
            <p className="text-sm text-neutral-600 flex-1 mb-6">
              {tool.description}
            </p>
            
            <div className="pt-4 border-t border-neutral-100 flex items-center justify-end space-x-3">
              <button className="text-sm font-medium text-neutral-600 hover:text-neutral-900">
                Configure
              </button>
              <button 
                className={`text-sm font-medium px-3 py-1.5 rounded-md transition-colors ${
                  tool.status === 'active' 
                    ? 'bg-red-50 text-red-600 hover:bg-red-100' 
                    : 'bg-indigo-50 text-indigo-600 hover:bg-indigo-100'
                }`}
              >
                {tool.status === 'active' ? 'Disable' : 'Enable'}
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
