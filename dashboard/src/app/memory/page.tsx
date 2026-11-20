import { revalidatePath } from "next/cache";
import { Brain, Pin, Trash2, Tag, ShieldAlert, Sparkles, Filter } from "lucide-react";
import {
  getMe,
  graphqlFetch,
  type TopicMemory,
  type EntityProfile,
} from "@/lib/graphql";
import { householdTopics, topicLabel } from "@/lib/packs";

export const dynamic = "force-dynamic";

const GET_MEMORY_DATA_QUERY = `
  query GetMemoryData($topic: String, $includeSuperseded: Boolean) {
    topicMemories(topic: $topic, includeSuperseded: $includeSuperseded) {
      id
      topic
      subtopic
      fact
      tagsJson
      scope
      isPinned
      supersededById
      validFrom
      validUntil
      createdAt
    }
    entityProfiles(topic: $topic) {
      id
      topic
      entityType
      entityName
      attributesJson
      updatedAt
    }
  }
`;

const PIN_MEMORY_MUTATION = `
  mutation PinMemory($id: ID!, $isPinned: Boolean!) {
    pinTopicMemory(id: $id, isPinned: $isPinned) { id }
  }
`;

const DELETE_MEMORY_MUTATION = `
  mutation DeleteMemory($id: ID!) {
    deleteTopicMemory(id: $id)
  }
`;

async function getMemoriesAndProfiles(
  selectedTopic?: string,
  showSuperseded?: boolean
): Promise<{ memories: TopicMemory[]; profiles: EntityProfile[] }> {
  try {
    const data = await graphqlFetch<{
      topicMemories: TopicMemory[];
      entityProfiles: EntityProfile[];
    }>(GET_MEMORY_DATA_QUERY, {
      topic: selectedTopic && selectedTopic !== "all" ? selectedTopic : null,
      includeSuperseded: !!showSuperseded,
    });

    return {
      memories: data.topicMemories || [],
      profiles: data.entityProfiles || [],
    };
  } catch (err) {
    console.error("Failed to fetch memory data:", err);
    return { memories: [], profiles: [] };
  }
}

export default async function MemoryExplorerPage({
  searchParams,
}: {
  searchParams?: Promise<{ topic?: string; superseded?: string }>;
}) {
  const params = await searchParams;
  const currentTopic = params?.topic || "all";
  const showSuperseded = params?.superseded === "true";

  const { memories, profiles } = await getMemoriesAndProfiles(currentTopic, showSuperseded);

  // V4-27: the household's topics (Core plus its packs').
  const me = await getMe();
  const topics = [
    { id: "all", label: "All Topics" },
    ...householdTopics(me).map((t) => ({ id: t, label: topicLabel(t) })),
  ];

  async function togglePin(formData: FormData) {
    "use server";
    const id = formData.get("id") as string;
    const currentPinned = formData.get("isPinned") === "true";
    try {
      await graphqlFetch(PIN_MEMORY_MUTATION, { id, isPinned: !currentPinned });
    } catch (err) {
      console.error("togglePin failed:", err);
    }
    revalidatePath("/memory");
  }

  async function deleteMemory(formData: FormData) {
    "use server";
    const id = formData.get("id") as string;
    try {
      await graphqlFetch(DELETE_MEMORY_MUTATION, { id });
    } catch (err) {
      console.error("deleteMemory failed:", err);
    }
    revalidatePath("/memory");
  }

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-semibold text-neutral-900 flex items-center">
            <Brain className="mr-3 h-7 w-7 text-indigo-600" />
            Memory Explorer
          </h1>
          <p className="text-sm text-neutral-500 mt-1">
            Hierarchical topic-mapped memory, living entity profiles, and conflict resolution history.
          </p>
        </div>
      </div>

      {/* Topic Filter Pills & Superseded Toggle */}
      <div className="flex flex-wrap items-center justify-between gap-4 mb-8 bg-white p-4 rounded-xl border border-neutral-200 shadow-sm">
        <div className="flex flex-wrap items-center gap-2">
          <Filter className="w-4 h-4 text-neutral-400 mr-1" />
          {topics.map((t) => (
            <a
              key={t.id}
              href={`/memory?topic=${t.id}${showSuperseded ? "&superseded=true" : ""}`}
              className={`px-3 py-1.5 text-xs font-medium rounded-full transition-colors ${
                currentTopic === t.id
                  ? "bg-indigo-600 text-white"
                  : "bg-neutral-100 text-neutral-600 hover:bg-neutral-200"
              }`}
            >
              {t.label}
            </a>
          ))}
        </div>

        <div className="flex items-center space-x-2">
          <a
            href={`/memory?topic=${currentTopic}${showSuperseded ? "" : "&superseded=true"}`}
            className={`flex items-center px-3 py-1.5 text-xs font-medium rounded-lg border transition-colors ${
              showSuperseded
                ? "bg-amber-50 text-amber-800 border-amber-300"
                : "bg-white text-neutral-600 border-neutral-200 hover:bg-neutral-50"
            }`}
          >
            <ShieldAlert className="w-3.5 h-3.5 mr-1.5" />
            {showSuperseded ? "Showing Superseded Memories" : "Show Superseded"}
          </a>
        </div>
      </div>

      {/* Living Entity Profiles Section */}
      {profiles.length > 0 && (
        <div className="mb-8">
          <h2 className="text-lg font-medium text-neutral-900 mb-4 flex items-center">
            <Sparkles className="w-5 h-5 mr-2 text-indigo-500" />
            Living Entity Profiles (Topic Knowledge Graph)
          </h2>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {profiles.map((p: EntityProfile) => {
              let attrs: Record<string, unknown> = {};
              try {
                attrs = JSON.parse(p.attributesJson || "{}");
              } catch {}

              return (
                <div key={p.id} className="bg-white rounded-xl p-5 border border-neutral-200 shadow-sm">
                  <div className="flex items-start justify-between mb-3">
                    <div>
                      <span className="text-xs font-semibold uppercase px-2 py-0.5 rounded bg-indigo-50 text-indigo-600">
                        {p.topic} • {p.entityType}
                      </span>
                      <h3 className="text-base font-semibold text-neutral-900 mt-2">{p.entityName}</h3>
                    </div>
                  </div>
                  <div className="bg-neutral-50 rounded-lg p-3 text-xs space-y-1 font-mono text-neutral-700 max-h-40 overflow-y-auto">
                    {Object.keys(attrs).length > 0 ? (
                      Object.entries(attrs).map(([k, v]) => (
                        <div key={k} className="flex justify-between">
                          <span className="text-neutral-500">{k}:</span>
                          <span className="font-medium text-neutral-900 truncate max-w-[150px]">
                            {typeof v === "object" ? JSON.stringify(v) : String(v)}
                          </span>
                        </div>
                      ))
                    ) : (
                      <span className="text-neutral-400">No structured attributes</span>
                    )}
                  </div>
                  <div className="mt-3 text-[11px] text-neutral-400">
                    Updated {new Date(p.updatedAt).toLocaleDateString()}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* Memories Table */}
      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
        <div className="p-6 border-b border-neutral-200 flex items-center justify-between">
          <h2 className="text-lg font-medium text-neutral-900">
            Topic Memories ({memories.length})
          </h2>
          <span className="text-xs text-neutral-400">
            Temporal facts with automatic superseding and topic tags
          </span>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="bg-neutral-50 text-neutral-500">
              <tr>
                <th className="px-6 py-3 font-medium">Topic / Subtopic</th>
                <th className="px-6 py-3 font-medium">Fact / Knowledge</th>
                <th className="px-6 py-3 font-medium">Tags</th>
                <th className="px-6 py-3 font-medium">Status</th>
                <th className="px-6 py-3 font-medium">Created</th>
                <th className="px-6 py-3 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-200">
              {memories.map((m: TopicMemory) => {
                let tags: string[] = [];
                try {
                  tags = JSON.parse(m.tagsJson || "[]");
                } catch {}

                const isSuperseded = !!m.supersededById;

                return (
                  <tr key={m.id} className={`hover:bg-neutral-50 ${isSuperseded ? "opacity-60 bg-neutral-50/50" : ""}`}>
                    <td className="px-6 py-4">
                      <div className="font-medium text-neutral-900 capitalize">{m.topic}</div>
                      <div className="text-xs text-neutral-500">{m.subtopic || "general"}</div>
                    </td>
                    <td className="px-6 py-4 text-neutral-800 max-w-md">
                      <div className="flex items-start">
                        {m.isPinned && (
                          <Pin className="w-4 h-4 text-indigo-600 mr-1.5 shrink-0 mt-0.5 fill-indigo-600" />
                        )}
                        <span>{m.fact}</span>
                      </div>
                    </td>
                    <td className="px-6 py-4">
                      <div className="flex flex-wrap gap-1">
                        {tags.map((t, idx) => (
                          <span
                            key={idx}
                            className="inline-flex items-center text-[10px] bg-neutral-100 text-neutral-600 px-2 py-0.5 rounded-full"
                          >
                            <Tag className="w-2.5 h-2.5 mr-1 text-neutral-400" />
                            {t}
                          </span>
                        ))}
                        {tags.length === 0 && <span className="text-xs text-neutral-400">—</span>}
                      </div>
                    </td>
                    <td className="px-6 py-4">
                      {isSuperseded ? (
                        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-amber-100 text-amber-800">
                          Superseded
                        </span>
                      ) : (
                        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-green-100 text-green-800">
                          Active
                        </span>
                      )}
                    </td>
                    <td className="px-6 py-4 text-xs text-neutral-500">
                      {new Date(m.createdAt).toLocaleDateString()}
                    </td>
                    <td className="px-6 py-4 text-right">
                      <div className="flex items-center justify-end space-x-2">
                        <form action={togglePin}>
                          <input type="hidden" name="id" value={m.id} />
                          <input type="hidden" name="isPinned" value={String(m.isPinned)} />
                          <button
                            type="submit"
                            title={m.isPinned ? "Unpin memory" : "Pin memory"}
                            className={`p-1.5 rounded hover:bg-neutral-100 ${
                              m.isPinned ? "text-indigo-600" : "text-neutral-400"
                            }`}
                          >
                            <Pin className="w-4 h-4" />
                          </button>
                        </form>
                        <form action={deleteMemory}>
                          <input type="hidden" name="id" value={m.id} />
                          <button
                            type="submit"
                            title="Delete memory"
                            className="p-1.5 rounded text-neutral-400 hover:text-red-600 hover:bg-neutral-100"
                          >
                            <Trash2 className="w-4 h-4" />
                          </button>
                        </form>
                      </div>
                    </td>
                  </tr>
                );
              })}
              {memories.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-6 py-12 text-center text-neutral-500">
                    No topic memories found. Memories created in chat or background cycles will appear here.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
