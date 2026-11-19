/**
 * Unified GraphQL Client for MyPA Dashboard
 * Replaces direct pg.Pool and raw SQL queries across all dashboard pages.
 */

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
 * The family group this dashboard manages. The dashboard's API secret is an admin bound to
 * exactly this family group on the server (P3-2), so every query/mutation must use the same
 * value: DASHBOARD_FAMILY_GROUP (server-side env, shared with the app).
 * Falls back to the demo family when unset (the server rejects any mismatch).
 */
export function getDashboardFamilyGroup(): string {
  const fg = process.env.DASHBOARD_FAMILY_GROUP?.trim();
  return fg ? fg : "Smith Family";
}

/**
 * Executes a typed GraphQL query or mutation against the app's /graphql.
 */
export async function graphqlFetch<TData = unknown, TVariables = Record<string, unknown>>(
  query: string,
  variables?: TVariables,
  init?: RequestInit
): Promise<TData> {
  const endpoint = getGraphQLEndpoint();
  const secret = process.env.DASHBOARD_API_SECRET || "";

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    // Always supply Authorization header to satisfy the app's edge guard and auth_middleware
    Authorization: `Bearer ${secret || "dev-dashboard-token"}`,
    ...(init?.headers as Record<string, string>),
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

// ----------------- Domain Entity Interfaces -----------------

export interface User {
  id: string;
  platformId: string;
  name: string;
  role: string;
  familyGroup: string;
  createdAt: string;
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
