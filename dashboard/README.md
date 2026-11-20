# MyPA — Web Control Dashboard (`dashboard/`)

The **MyPA Web Control Dashboard** is a Next.js 16 (App Router) and React 19 administrative and family life-management console. It provides real-time visibility and interactive controls across all 9 MyPA life domains, multi-tenant user onboarding, OAuth connections, telemetry logs, and one-click ACID snapshot rollbacks.

---

## Architecture & Data Layer

- **Framework:** Next.js 16.3 (`Turbopack`), React 19, TypeScript 5, Tailwind CSS v4, Lucide Icons.
- **Zero Direct Database Access:** The dashboard is completely decoupled from PostgreSQL. All server components, client views, and mutations communicate exclusively with the app's `gqlgen` GraphQL API, which the dashboard calls directly (`http://app:8000/graphql`, V4-01).
- **Sign-in (V4-35, `src/lib/session.ts`, `src/lib/auth_routes.ts`, `src/proxy.ts`, `src/app/signin`):** each owner or adult signs in with a link from `/dashboard` in chat. The page reads the token after `#` and posts it when the button is pressed; `/api/auth/signin` exchanges it at the app for a session kept in an `HttpOnly`, `SameSite=Lax` cookie (`__Host-` and `Secure` on HTTPS). Without a session cookie every page goes to `/signin`. There is no shared dashboard secret.
- **Typed GraphQL Client (`src/lib/graphql.ts`):** Centralizes `graphqlFetch<TData, TVariables>`, which sends the signed-in person's session as the bearer value; no page sends a household, SSR/client endpoint resolution, and full TypeScript domain interfaces.
- **Resilience & Empty States:** Includes a global React Client Error Boundary (`src/app/error.tsx`) and zero hardcoded mock data—all views render clean onboarding states when records are empty.

---

## Console Routes

| Route | Console | Key Capabilities & GraphQL Operations |
| :--- | :--- | :--- |
| `/` | **Overview** | System health KPIs, domain summaries, and quick navigation cards |
| `/memory` | **Memory Explorer** | Browse, filter, pin, and delete `TopicMemory` facts and `EntityProfile` cards |
| `/shared` | **Shared Spaces** | Configure family topic permissions (`CanView`, `CanEdit`), notification modes (`instant`, `digest`, `silent`), and shared reminders |
| `/baby` | **Baby Tracker** | Baby profiles, feeding/sleep/diaper/temperature/medication logs, developmental milestones, and supply inventory |
| `/house` | **House & Vehicles** | Leases, warranties, insurance contracts, preventive home maintenance checklists, and vehicle asset vault |
| `/finance` | **Finance & Budgets** | Expense/income transaction ledger, monthly category budget progress bars, and recurring bills |
| `/automations` | **Automations & CRM** | SSRF-guarded external REST API connectors, `When -> If -> Then` `EventBus` rules, user persona preferences, and Personal CRM |
| `/audit` | **Audit & Rollback** | Immutable `MutationLog` inspector (`BeforeJSON` / `AfterJSON` diffs) with one-click transactional **Rollback** (`rollbackMutation`) |
| `/sessions` | **User Sessions** | Multi-channel conversation session summaries across Telegram and WhatsApp |
| `/logs` | **Telemetry Logs** | End-to-end `AuditSession` latency, token usage, and execution status traces |
| `/users` | **Users & Pairing** | The household's members and their roles, and 6-digit pairing codes for an adult or a child (owners); an adult sees only themselves |
| `/signin` | **Sign in** | Signs in with the link from `/dashboard` in chat |
| `/oauth` | **OAuth Connections** | Google Workspace (Calendar, Gmail, Tasks, Contacts) OAuth connection status |
| `/tools` | **AI Tool Catalog** | Interactive directory of all 70 registered `ToolModule` declarations |
| `/settings` | **System Settings** | Runtime configuration and environment overview |

---

## Environment Variables

| Variable | Description | Default |
| :--- | :--- | :--- |
| `GRAPHQL_ENDPOINT` | Server-side GraphQL URL used by Next.js Server Components | `http://app:8000/graphql` |
| `NEXT_PUBLIC_APP_URL` | Client-side base URL of the app for browser GraphQL requests | `http://localhost:8000` |
| `NEXT_PUBLIC_GRAPHQL_API_KEY` | Client-side Bearer token for browser mutations | *(from root `.env`)* |

---

## Development & Testing

```bash
# Install dependencies
npm install

# Start development server on http://localhost:3000
npm run dev

# Run Vitest unit & React Testing Library component suites (46 tests across 6 suites)
npm test

# Build production standalone bundle
npm run build
```

When running via Docker Compose (`docker-compose up -d --build`), the standalone Next.js container is exposed on host port **`http://localhost:3030`**.
