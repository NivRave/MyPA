import { revalidatePath } from "next/cache";
import { Share2, Users, Bell, CheckCircle2, Clock, Plus, ShieldCheck } from "lucide-react";
import {
  graphqlFetch,
  getDashboardFamilyGroup,
  type SharedSpace,
  type SharedTaskReminder,
} from "@/lib/graphql";

export const dynamic = "force-dynamic";

const GET_SHARED_DATA_QUERY = `
  query GetSharedSpaces($familyGroup: String!) {
    sharedSpaces(familyGroup: $familyGroup) {
      id
      familyGroup
      topic
      ownerUserId
      sharedWithUserId
      canView
      canEdit
      notifyMode
      updatedAt
    }
    sharedTasks(familyGroup: $familyGroup) {
      id
      familyGroup
      topic
      title
      dueTime
      createdByUserId
      assignedToUserId
      isCompleted
      completedByUserId
      completedAt
      createdAt
    }
  }
`;

const UPDATE_SHARED_SPACE_MUTATION = `
  mutation UpdateSpace(
    $familyGroup: String!
    $topic: String!
    $sharedWithUserId: String!
    $canView: Boolean!
    $canEdit: Boolean!
    $notifyMode: String!
  ) {
    updateSharedSpace(
      familyGroup: $familyGroup
      topic: $topic
      sharedWithUserId: $sharedWithUserId
      canView: $canView
      canEdit: $canEdit
      notifyMode: $notifyMode
    ) {
      id
    }
  }
`;

const COMPLETE_TASK_MUTATION = `
  mutation CompleteTask($id: ID!, $completedByUserId: String!) {
    completeSharedTask(id: $id, completedByUserId: $completedByUserId) {
      id
    }
  }
`;

const CREATE_TASK_MUTATION = `
  mutation CreateTask(
    $familyGroup: String!
    $topic: String!
    $title: String!
    $assignedToUserId: String
  ) {
    createSharedTask(
      familyGroup: $familyGroup
      topic: $topic
      title: $title
      assignedToUserId: $assignedToUserId
    ) {
      id
    }
  }
`;

async function getSharedData(familyGroup: string = getDashboardFamilyGroup()): Promise<{
  spaces: SharedSpace[];
  tasks: SharedTaskReminder[];
}> {
  try {
    const data = await graphqlFetch<{
      sharedSpaces: SharedSpace[];
      sharedTasks: SharedTaskReminder[];
    }>(GET_SHARED_DATA_QUERY, { familyGroup });

    return {
      spaces: data.sharedSpaces || [],
      tasks: data.sharedTasks || [],
    };
  } catch (err) {
    console.error("Failed to fetch shared data:", err);
    return { spaces: [], tasks: [] };
  }
}

export default async function SharedSpacesPage() {
  const familyGroup = getDashboardFamilyGroup();
  const { spaces, tasks } = await getSharedData(familyGroup);

  async function updatePermission(formData: FormData) {
    "use server";
    const topic = (formData.get("topic") as string) || "general";
    const sharedWithUserId = (formData.get("sharedWithUserId") as string) || "*";
    const canView = formData.get("canView") === "on";
    const canEdit = formData.get("canEdit") === "on";
    const notifyMode = (formData.get("notifyMode") as string) || "instant";

    try {
      await graphqlFetch(UPDATE_SHARED_SPACE_MUTATION, {
        familyGroup: getDashboardFamilyGroup(),
        topic,
        sharedWithUserId,
        canView,
        canEdit,
        notifyMode,
      });
    } catch (err) {
      console.error("updatePermission failed:", err);
    }
    revalidatePath("/shared");
  }

  async function completeTask(formData: FormData) {
    "use server";
    const id = formData.get("id") as string;
    try {
      await graphqlFetch(COMPLETE_TASK_MUTATION, {
        id,
        completedByUserId: "partner",
      });
    } catch (err) {
      console.error("completeTask failed:", err);
    }
    revalidatePath("/shared");
  }

  async function addTask(formData: FormData) {
    "use server";
    const topic = (formData.get("topic") as string) || "general";
    const title = (formData.get("title") as string) || "";
    const assignedTo = (formData.get("assignedTo") as string) || null;

    try {
      await graphqlFetch(CREATE_TASK_MUTATION, {
        familyGroup: getDashboardFamilyGroup(),
        topic,
        title,
        assignedToUserId: assignedTo,
      });
    } catch (err) {
      console.error("addTask failed:", err);
    }
    revalidatePath("/shared");
  }

  return (
    <div className="flex-1 p-8 overflow-y-auto">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-semibold text-neutral-900 flex items-center">
            <Share2 className="mr-3 h-7 w-7 text-blue-600" />
            Shared Spaces & Partner Sync
          </h1>
          <p className="text-sm text-neutral-500 mt-1">
            Topic-level sharing permissions, instant cross-user notifications, and collaborative tasks.
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-8 mb-8">
        {/* Family Group Card */}
        <div className="bg-white rounded-xl shadow-sm border border-neutral-200 p-6 flex flex-col justify-between">
          <div>
            <div className="flex items-center space-x-3 mb-4">
              <div className="p-3 bg-blue-50 text-blue-600 rounded-lg">
                <Users className="w-6 h-6" />
              </div>
              <div>
                <h3 className="font-semibold text-neutral-900">{familyGroup}</h3>
                <span className="text-xs text-green-600 font-medium">● 2 Active Members Linked</span>
              </div>
            </div>
            <p className="text-xs text-neutral-500 mb-4">
              Events, baby routines, and house tasks are synchronized between the connected chat accounts.
            </p>
          </div>
          <div className="bg-neutral-50 rounded-lg p-3 text-xs space-y-1.5 border border-neutral-100">
            <div className="flex justify-between">
              <span className="text-neutral-500">Primary Partner:</span>
              <span className="font-medium text-neutral-800">Dana (Admin)</span>
            </div>
            <div className="flex justify-between">
              <span className="text-neutral-500">Secondary Partner:</span>
              <span className="font-medium text-neutral-800">Omer (Family)</span>
            </div>
          </div>
        </div>

        {/* Update Permissions Form */}
        <div className="lg:col-span-2 bg-white rounded-xl shadow-sm border border-neutral-200 p-6">
          <h2 className="text-base font-semibold text-neutral-900 mb-4 flex items-center">
            <ShieldCheck className="w-5 h-5 mr-2 text-blue-600" />
            Configure Topic Sharing Rule
          </h2>
          <form action={updatePermission} className="grid grid-cols-1 md:grid-cols-3 gap-4 text-sm">
            <div>
              <label className="block text-xs font-medium text-neutral-700 mb-1">Topic</label>
              <select name="topic" className="w-full border border-neutral-300 rounded-md p-2 text-sm bg-white">
                <option value="baby">Baby Care</option>
                <option value="house">House & Maintenance</option>
                <option value="finance">Family Finance</option>
                <option value="schedule">Schedule & Calendar</option>
                <option value="health">Family Health</option>
              </select>
            </div>
            <div>
              <label className="block text-xs font-medium text-neutral-700 mb-1">Notification Mode</label>
              <select name="notifyMode" className="w-full border border-neutral-300 rounded-md p-2 text-sm bg-white">
                <option value="instant">Instant Push (in chat)</option>
                <option value="digest">Smart Daily Digest</option>
                <option value="silent">Silent Sync (Query Only)</option>
              </select>
            </div>
            <div>
              <label className="block text-xs font-medium text-neutral-700 mb-1">Permissions</label>
              <div className="flex items-center space-x-4 mt-2">
                <label className="inline-flex items-center text-xs text-neutral-700">
                  <input type="checkbox" name="canView" defaultChecked className="mr-1.5 rounded" /> Can View
                </label>
                <label className="inline-flex items-center text-xs text-neutral-700">
                  <input type="checkbox" name="canEdit" defaultChecked className="mr-1.5 rounded" /> Can Edit
                </label>
              </div>
            </div>
            <div className="md:col-span-3 flex justify-end pt-2">
              <button
                type="submit"
                className="px-4 py-2 bg-blue-600 text-white text-xs font-medium rounded-md hover:bg-blue-700 transition-colors"
              >
                Save Topic Rule
              </button>
            </div>
          </form>
        </div>
      </div>

      {/* Permissions Table */}
      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden mb-8">
        <div className="p-6 border-b border-neutral-200">
          <h2 className="text-lg font-medium text-neutral-900">Active Topic Sharing Permissions</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="bg-neutral-50 text-neutral-500">
              <tr>
                <th className="px-6 py-3 font-medium">Topic</th>
                <th className="px-6 py-3 font-medium">Shared Scope</th>
                <th className="px-6 py-3 font-medium">View / Edit</th>
                <th className="px-6 py-3 font-medium">Notification Mode</th>
                <th className="px-6 py-3 font-medium">Last Updated</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-200">
              {spaces.map((s: SharedSpace) => (
                <tr key={s.id} className="hover:bg-neutral-50">
                  <td className="px-6 py-4 font-medium text-neutral-900 capitalize">{s.topic}</td>
                  <td className="px-6 py-4 text-neutral-600">
                    {s.sharedWithUserId === "*" ? "Entire Family Group" : s.sharedWithUserId}
                  </td>
                  <td className="px-6 py-4">
                    <span className="inline-flex items-center text-xs space-x-1.5">
                      <span className={`px-2 py-0.5 rounded ${s.canView ? "bg-green-100 text-green-800" : "bg-neutral-100 text-neutral-400"}`}>View</span>
                      <span className={`px-2 py-0.5 rounded ${s.canEdit ? "bg-green-100 text-green-800" : "bg-neutral-100 text-neutral-400"}`}>Edit</span>
                    </span>
                  </td>
                  <td className="px-6 py-4">
                    <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium capitalize bg-blue-50 text-blue-700">
                      <Bell className="w-3 h-3 mr-1" />
                      {s.notifyMode}
                    </span>
                  </td>
                  <td className="px-6 py-4 text-xs text-neutral-500">
                    {new Date(s.updatedAt).toLocaleDateString()}
                  </td>
                </tr>
              ))}
              {spaces.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-6 py-8 text-center text-neutral-500">
                    No custom rules defined. Topics default to shared family access (Baby, House, Finance).
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Shared Tasks & Reminders */}
      <div className="bg-white rounded-xl shadow-sm border border-neutral-200 overflow-hidden">
        <div className="p-6 border-b border-neutral-200 flex items-center justify-between">
          <div>
            <h2 className="text-lg font-medium text-neutral-900">Collaborative Reminders & Tasks</h2>
            <p className="text-xs text-neutral-500 mt-0.5">When one partner completes a task, alerts are automatically resolved for both.</p>
          </div>
          <form action={addTask} className="flex items-center space-x-2">
            <select name="topic" className="border border-neutral-300 rounded-md p-1.5 text-xs bg-white">
              <option value="baby">Baby</option>
              <option value="house">House</option>
              <option value="finance">Finance</option>
            </select>
            <input
              type="text"
              name="title"
              placeholder="e.g. Give Vitamin D drops"
              className="border border-neutral-300 rounded-md p-1.5 text-xs w-48"
              required
            />
            <button
              type="submit"
              className="flex items-center px-3 py-1.5 bg-neutral-900 text-white rounded-md text-xs font-medium hover:bg-neutral-800"
            >
              <Plus className="w-3.5 h-3.5 mr-1" /> Add Task
            </button>
          </form>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="bg-neutral-50 text-neutral-500">
              <tr>
                <th className="px-6 py-3 font-medium">Status</th>
                <th className="px-6 py-3 font-medium">Task</th>
                <th className="px-6 py-3 font-medium">Topic</th>
                <th className="px-6 py-3 font-medium">Due Date</th>
                <th className="px-6 py-3 font-medium">Assigned To</th>
                <th className="px-6 py-3 font-medium text-right">Action</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-200">
              {tasks.map((t: SharedTaskReminder) => (
                <tr key={t.id} className={`hover:bg-neutral-50 ${t.isCompleted ? "opacity-60 bg-neutral-50/50" : ""}`}>
                  <td className="px-6 py-4">
                    {t.isCompleted ? (
                      <span className="inline-flex items-center text-xs text-green-600 font-medium">
                        <CheckCircle2 className="w-4 h-4 mr-1 text-green-500" /> Done
                      </span>
                    ) : (
                      <span className="inline-flex items-center text-xs text-amber-600 font-medium">
                        <Clock className="w-4 h-4 mr-1 text-amber-500" /> Pending
                      </span>
                    )}
                  </td>
                  <td className="px-6 py-4 font-medium text-neutral-900">
                    <span className={t.isCompleted ? "line-through text-neutral-500" : ""}>{t.title}</span>
                  </td>
                  <td className="px-6 py-4 capitalize text-neutral-600">{t.topic}</td>
                  <td className="px-6 py-4 text-xs text-neutral-500">
                    {t.dueTime ? new Date(t.dueTime).toLocaleString() : "No deadline"}
                  </td>
                  <td className="px-6 py-4 text-xs text-neutral-600">
                    {t.assignedToUserId || "Anyone"}
                  </td>
                  <td className="px-6 py-4 text-right">
                    {!t.isCompleted && (
                      <form action={completeTask} className="inline-block">
                        <input type="hidden" name="id" value={t.id} />
                        <button
                          type="submit"
                          className="px-3 py-1 bg-green-50 text-green-700 hover:bg-green-100 rounded text-xs font-medium transition-colors"
                        >
                          Mark Done
                        </button>
                      </form>
                    )}
                  </td>
                </tr>
              ))}
              {tasks.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-6 py-8 text-center text-neutral-500">
                    No active shared tasks.
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
