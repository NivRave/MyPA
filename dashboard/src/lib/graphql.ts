/**
 * Unified GraphQL Client for MyPA Dashboard
 * Replaces direct pg.Pool and raw SQL queries across all dashboard pages.
 */
import { sessionFrom } from "./session";

export interface GraphQLErrorLocation {
  line: number;
  column: number;
}

export interface GraphQLError {
  message: string;
  locations?: GraphQLErrorLocation[];
  path?: Array<string | number>;
  extensions?: Record<string, unknown>;
}

export interface GraphQLResponse<T> {
  data?: T;
  errors?: GraphQLError[];
}

export class GraphQLClientError extends Error {
  public errors?: GraphQLError[];
  public status?: number;

  constructor(message: string, errors?: GraphQLError[], status?: number) {
    super(message);
    this.name = "GraphQLClientError";
    this.errors = errors;
    this.status = status;
  }
}

/**
 * Resolves the target GraphQL endpoint based on execution context:
 * 1. Explicit GRAPHQL_ENDPOINT (e.g. from docker-compose or .env)
 * 2. APP_URL / NEXT_PUBLIC_APP_URL (the base address of the MyPA app)
 * 3. Fallback to container DNS http://app:8000/graphql (or http://localhost:8000/graphql)
 *
 * V4-01: the dashboard talks to the app directly; there is no proxy in between any more.
 */
export function getGraphQLEndpoint(): string {
  if (process.env.GRAPHQL_ENDPOINT) {
    return process.env.GRAPHQL_ENDPOINT;
  }
  if (process.env.APP_URL) {
    return `${process.env.APP_URL.replace(/\/+$/, "")}/graphql`;
  }
  if (process.env.NEXT_PUBLIC_APP_URL) {
    return `${process.env.NEXT_PUBLIC_APP_URL.replace(/\/+$/, "")}/graphql`;
  }
  // In Node/SSR environment inside Docker network vs client browser:
  return typeof window === "undefined"
    ? "http://app:8000/graphql"
    : "http://localhost:8000/graphql";
}

/**
 * The signed-in person's session secret, from the browser's cookie (V4-35). Server-side only:
 * `next/headers` is loaded on demand, so the module still imports in a test or a client bundle.
 */
async function currentSession(): Promise<string | undefined> {
  try {
    const { cookies } = await import("next/headers");
    return sessionFrom(await cookies());
  } catch {
    return undefined;
  }
}

/**
 * Executes a typed GraphQL query or mutation against the app's /graphql, as the signed-in person:
 * the session secret from the browser's cookie is sent as a bearer value (V4-35). There is no
 * shared dashboard secret any more. Without a session nothing is sent, and the error has status
 * 401, so the page can say how to sign in.
 */
export async function graphqlFetch<TData = unknown, TVariables = Record<string, unknown>>(
  query: string,
  variables?: TVariables,
  init?: RequestInit
): Promise<TData> {
  const endpoint = getGraphQLEndpoint();
  const session = await currentSession();
  if (!session) {
    throw new GraphQLClientError("Not signed in: send /dashboard to the assistant for a sign-in link", undefined, 401);
  }

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(init?.headers as Record<string, string>),
    Authorization: `Bearer ${session}`,
  };

  const response = await fetch(endpoint, {
    method: "POST",
    cache: "no-store",
    ...init,
    headers,
    body: JSON.stringify({ query, variables }),
  });

  if (!response.ok) {
    const errorBody = await response.text().catch(() => "");
    throw new GraphQLClientError(
      `GraphQL HTTP Error ${response.status}: ${errorBody || response.statusText}`,
      undefined,
      response.status
    );
  }

  const json: GraphQLResponse<TData> = await response.json();

  if (json.errors && json.errors.length > 0) {
    const message = json.errors.map((e) => e.message).join("; ");
    throw new GraphQLClientError(`GraphQL Error: ${message}`, json.errors);
  }

  if (json.data === undefined || json.data === null) {
    throw new GraphQLClientError("GraphQL returned empty data response");
  }

  return json.data;
}

/** A feature pack (V4-27): opt-in per household; Core is always on and is not a pack. */
export interface Pack {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  /** The dashboard routes the pack owns; hidden while it is off. */
  pages: string[];
}

/** The signed-in person (V4-35). */
export interface Me {
  personId: string;
  name: string;
  role: "owner" | "adult" | "child" | "guest" | string;
  isOperator: boolean;
  /** topics: Core's, then those of each pack the household has on (V4-27). */
  household: { id: string; name: string; packs: Pack[]; topics: string[] };
}

/** The signed-in person, or null when there is no valid session. */
export async function getMe(): Promise<Me | null> {
  try {
    const data = await graphqlFetch<{ me: Me }>(`query Me { me { personId name role isOperator household { id name packs { id name description enabled pages } topics } } }`);
    return data.me;
  } catch {
    return null;
  }
}

/** The signed-in person's household name, or "" when unknown. */
export async function getHouseholdName(): Promise<string> {
  try {
    const data = await graphqlFetch<{ household: { name: string } }>(`query HouseholdName { household { name } }`);
    return data.household.name;
  } catch {
    return "";
  }
}

// ----------------- Domain Entity Interfaces -----------------

export interface User {
  id: string;
  platformId: string;
  name: string;
  role: string;
  familyGroup: string;
  createdAt: string;
}

// BL-41: one of a member's channel accounts, as an owner sees it (Query.memberAccounts).
export interface MemberAccount {
  id: string;
  personId: string;
  channel: string;
  label: string;
  linkedAt: string;
}

// V4-41: what a new person does on one channel before their first message (Query.joinSteps).
export interface JoinStep {
  channel: string;
  step: string;
}

export interface PairingCode {
  id: string;
  code: string;
  role: string;
  familyGroup: string;
  expiresAt: string;
  isUsed: boolean;
  createdAt: string;
}

export interface MutationLog {
  id: string;
  userId: string;
  familyGroup: string;
  topic: string;
  action: string;
  entityType: string;
  entityId: string;
  beforeJson?: string | null;
  afterJson?: string | null;
  summary: string;
  isUndone: boolean;
  createdAt: string;
}

export interface ExternalAPIConnector {
  id: string;
  userId: string;
  familyGroup: string;
  name: string;
  baseUrl: string;
  defaultMethod: string;
  description: string;
  createdAt: string;
}

export interface AutomationRule {
  id: string;
  userId: string;
  familyGroup: string;
  name: string;
  triggerType: string;
  triggerTopic: string;
  conditionJson: string;
  actionInstruction: string;
  isActive: boolean;
  createdAt: string;
}

export interface UserPreference {
  userId: string;
  preferredLanguage: string;
  tonePersona: string;
  defaultCurrency: string;
  quietHoursStart: string;
  quietHoursEnd: string;
  briefingTopicsJson: string;
  standingInstructions: string;
  updatedAt: string;
}

export interface PersonalCRMContact {
  id: string;
  userId: string;
  familyGroup: string;
  personName: string;
  relationship: string;
  importantDatesJson: string;
  giftIdeasAndSizes: string;
  medicalNotes: string;
  updatedAt: string;
}

export interface BabyProfile {
  id: string;
  familyGroup: string;
  name: string;
  dateOfBirth: string;
  dueDate?: string | null;
  gender: string;
  birthWeightGrams: number;
  currentWeightGrams: number;
  heightCm: number;
  pediatricianInfo: string;
  allergiesNotes: string;
  updatedByUserId: string;
  updatedAt: string;
}

export interface BabyCareLog {
  id: string;
  babyId: string;
  familyGroup: string;
  logType: string;
  valueNumeric: number;
  unit: string;
  details: string;
  timestamp: string;
  loggedByUserId: string;
}

export interface BabyInventoryItem {
  id: string;
  familyGroup: string;
  itemName: string;
  quantityRemaining: number;
  unit: string;
  lowThreshold: number;
  updatedAt: string;
}

export interface FinancialTransaction {
  id: string;
  userId: string;
  familyGroup: string;
  type: string;
  category: string;
  merchant: string;
  amount: number;
  currency: string;
  transactionDate: string;
  isShared: boolean;
  source: string;
  notes: string;
  createdAt: string;
  /** BL-66: whether the viewer logged it; only they can change or delete it. */
  isMine: boolean;
}

export interface CategoryBudget {
  id: string;
  familyGroup: string;
  category: string;
  monthlyLimit: number;
  currency: string;
  alertThresholdPct: number;
  createdAt: string;
  updatedAt: string;
}

export interface RecurringBill {
  id: string;
  familyGroup: string;
  name: string;
  category: string;
  expectedAmount: number;
  currency: string;
  billingCycle: string;
  nextDueDay: number;
  autoRenew: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface HouseContractRecord {
  id: string;
  familyGroup: string;
  recordType: string;
  title: string;
  vendorOrLandlord: string;
  amount: number;
  currency: string;
  expirationOrRenewalDate?: string | null;
  noticePeriodDays: number;
  createdAt: string;
}

export interface HomeMaintenanceItem {
  id: string;
  familyGroup: string;
  taskName: string;
  category: string;
  intervalDays: number;
  lastCompletedAt?: string | null;
  nextDueAt: string;
  contractorInfo: string;
  notes: string;
}

export interface VehicleAsset {
  id: string;
  familyGroup: string;
  name: string;
  licensePlate: string;
  insuranceExpiry?: string | null;
  annualTestExpiry?: string | null;
  currentMileageKm: number;
  nextServiceMileageKm: number;
  notes: string;
}

export interface TopicMemory {
  id: string;
  userId: string;
  familyGroup: string;
  topic: string;
  subtopic: string;
  tagsJson: string;
  fact: string;
  scope: string;
  isPinned: boolean;
  supersededById?: number | null;
  validFrom: string;
  validUntil?: string | null;
  createdAt: string;
}

export interface EntityProfile {
  id: string;
  userId: string;
  familyGroup: string;
  topic: string;
  entityType: string;
  entityName: string;
  attributesJson: string;
  updatedAt: string;
}

export interface SharedSpace {
  id: string;
  familyGroup: string;
  topic: string;
  ownerUserId: string;
  sharedWithUserId: string;
  canView: boolean;
  canEdit: boolean;
  notifyMode: string;
  updatedAt: string;
}

export interface SharedTaskReminder {
  id: string;
  familyGroup: string;
  topic: string;
  title: string;
  dueTime?: string | null;
  createdByUserId: string;
  assignedToUserId?: string | null;
  isCompleted: boolean;
  completedByUserId?: string | null;
  completedAt?: string | null;
  createdAt: string;
}

export interface AuditSession {
  id: string;
  userId: string;
  source: string;
  userPrompt: string;
  status: string;
  totalDurationMs: number;
  startTime: string;
}

export interface UserSessionSummary {
  userId: string;
  source: string;
  totalInteractions: number;
  lastActive: string;
}

/** One person's chat model tokens on one day (V4-04). */
export interface UsageDay {
  day: string;
  personId: string;
  name: string;
  inputTokens: number;
  outputTokens: number;
  calls: number;
  turns: number;
}

/** The tokens of one turn (V4-04). */
export interface TurnUsage {
  turnId: string;
  personId: string;
  name: string;
  startedAt: string;
  inputTokens: number;
  outputTokens: number;
  calls: number;
  providers: string;
}

/** Chat model usage and the daily caps (V4-04). A cap of 0 means no cap. */
export interface Usage {
  timeZone: string;
  personDailyCap: number;
  installDailyCap: number;
  myTokensToday: number;
  /** The whole install's tokens today: the operator's only, null for anyone else. */
  installTokensToday: number | null;
  days: UsageDay[];
  turns: TurnUsage[];
}
