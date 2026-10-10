/**
 * The TanStack Query key factory and freshness policies of the web (plan W6; developer-spec.md
 * §10.7, Appendix E §E.6, which is the detailed source). Every hook builds its key here, so a key
 * never drifts between the hook that reads it, the mutation that invalidates it (`meta.invalidates`)
 * and the realtime event map (TW5).
 *
 * Conventions (Appendix E §E.6):
 * - A key starts with its domain; the second element is a params object or a sub-resource string
 *   (`'detail'`, `'maps'`, `'messages'`). Invalidating the domain prefix (`queryKeys.trucks.all()`)
 *   reaches every variant, so invalidate the narrowest prefix that covers the change.
 * - Filters live in the key (and in the request); an infinite list keeps its cursor as the
 *   `pageParam`, never in the key. Keys never change when a domain moves from Firestore to Go.
 * - Per-page shapes are `select` functions next to the hook, never a second key for the same data.
 */

const MINUTE = 60_000;

/** One row of the stale / gc table of developer-spec.md §10.7. */
export interface QueryPolicy {
    staleTime: number;
    gcTime: number;
}

/**
 * Stale and garbage-collection times per key family (developer-spec.md §10.7). The global default
 * (`QUERY_DEFAULTS` in lib/queryClient.ts) is 30 s / 5 min; a hook spreads the row of its key.
 */
export const QUERY_POLICY = {
    /** `['me']` */
    me: { staleTime: 5 * MINUTE, gcTime: 30 * MINUTE },
    /** `['webFlags']` (T17, `features/platform/api/webFlags.ts`) */
    webFlags: { staleTime: MINUTE, gcTime: 30 * MINUTE },
    /** hubs, hub maps, customers, subcontractors, companies, holidays, compConfig, roles, distances, rate-card tables */
    masterData: { staleTime: 10 * MINUTE, gcTime: 60 * MINUTE },
    /** detail keys, drivers, trucks, renewals, truck assignments, billing, income, statements, mobile settings */
    entity: { staleTime: 5 * MINUTE, gcTime: 30 * MINUTE },
    /** tasks, monitor trips, standby, incidents, badges, security events */
    live: { staleTime: 30_000, gcTime: 5 * MINUTE },
    /** expenses, maintenance, leave, payroll, broadcasts, users, installations, waitlist, dashboard */
    list: { staleTime: MINUTE, gcTime: 5 * MINUTE },
    /** chats */
    chat: { staleTime: 15_000, gcTime: 5 * MINUTE },
    /** fuel monthly / daily / Bangchak retail prices */
    fuel: { staleTime: 60 * MINUTE, gcTime: 120 * MINUTE },
} as const satisfies Record<string, QueryPolicy>;

type Params = Readonly<Record<string, unknown>>;

/** `[domain]` alone or `[domain, params]`: a list key whose params are optional. */
function listKey<D extends string, P extends Params>(domain: D, params?: P) {
    return (params === undefined ? [domain] : [domain, params]) as readonly [D] | readonly [D, P];
}

export const queryKeys = {
    me: () => ["me"] as const,
    /** `GET /v1/me/tenants` (the tenant switcher, T18): under the `['me']` prefix, same policy row. */
    myTenants: () => ["me", "tenants"] as const,
    webFlags: () => ["webFlags"] as const,
    /** Sidebar and dashboard counters (`GET /v1/badges`; P0: one count query, Appendix E §E.5 row 19). */
    badges: () => ["badges"] as const,
    jobs: (id: string) => ["jobs", id] as const,

    hubs: {
        all: () => ["hubs"] as const,
        /** `nameToCode` and `codeToName`: two objects, never merged (.vibe-rules.md billing rule). */
        maps: () => ["hubs", "maps"] as const,
    },
    distances: {
        all: () => ["distances"] as const,
        byHub: (hubId: string) => ["distances", { hubId }] as const,
        meta: () => ["distances", "meta"] as const,
    },
    customers: {
        all: () => ["customers"] as const,
        list: <P extends Params>(params?: P) => listKey("customers", params),
        detail: (id: string) => ["customers", "detail", id] as const,
    },
    subcontractors: {
        all: () => ["subcontractors"] as const,
        list: <P extends Params>(params?: P) => listKey("subcontractors", params),
        detail: (id: string) => ["subcontractors", "detail", id] as const,
    },
    companies: {
        all: () => ["companies"] as const,
        owner: () => ["companies", { owner: true }] as const,
    },
    holidays: {
        all: () => ["holidays"] as const,
        year: (year: number) => ["holidays", year] as const,
    },
    compConfig: (which: "active" | "all") => ["compConfig", which] as const,
    roles: {
        all: () => ["roles"] as const,
        matrix: () => ["roles", "matrix"] as const,
    },

    drivers: {
        all: () => ["drivers"] as const,
        list: <P extends Params>(params: P) => ["drivers", params] as const,
        detail: (id: string) => ["drivers", "detail", id] as const,
    },
    trucks: {
        all: () => ["trucks"] as const,
        list: <P extends Params>(params: P) => ["trucks", params] as const,
        detail: (id: string) => ["trucks", "detail", id] as const,
    },
    renewals: <P extends Params>(params: P) => ["renewals", params] as const,
    truckAssignments: <P extends Params>(params: P) => ["truckAssignments", params] as const,

    tasks: {
        all: () => ["tasks"] as const,
        list: <P extends Params>(params: P) => ["tasks", params] as const,
        detail: (id: string) => ["tasks", "detail", id] as const,
        trip: (taskId: string) => ["tasks", "trip", taskId] as const,
    },
    trips: {
        all: () => ["trips"] as const,
        monitor: <P extends Params>(params: P) => ["trips", "monitor", params] as const,
        list: <P extends Params>(params: P) => ["trips", params] as const,
        detail: (id: string) => ["trips", "detail", id] as const,
        photos: (id: string) => ["trips", "photos", id] as const,
    },
    standby: <P extends Params>(params?: P) => listKey("standby", params),
    incidents: <P extends Params>(params?: P) => listKey("incidents", params),
    vehicleLocations: () => ["vehicleLocations"] as const,
    dashboard: {
        summary: <P extends Params>(params: P) => ["dashboard", "summary", params] as const,
    },

    billing: {
        rows: (customerId: string, year: number, month: number) => ["billing", "rows", customerId, year, month] as const,
        standbyDiag: (customerId: string, year: number, month: number) =>
            ["billing", "standbyDiag", customerId, year, month] as const,
        missingDate: (customerId: string, year: number, month: number) =>
            ["billing", "missingDate", customerId, year, month] as const,
        shopee: (customerId: string, year: number, month: number, half: 1 | 2) =>
            ["billing", "shopee", customerId, year, month, half] as const,
    },
    income: <P extends Params>(params: P) => ["income", params] as const,
    rateCard: {
        all: () => ["rateCard"] as const,
        entries: (customerId: string) => ["rateCard", "entries", customerId] as const,
        fuelAdj: (customerId: string) => ["rateCard", "fuelAdj", customerId] as const,
        serviceFees: () => ["rateCard", "serviceFees"] as const,
        standby: () => ["rateCard", "standby"] as const,
    },
    fuel: {
        monthly: () => ["fuel", "monthly"] as const,
        daily: (day: string | { from: string }) => ["fuel", "daily", day] as const,
        /** Bangchak retail prices in one language (`GET /v1/fuel/retail?locale=`). */
        bangchak: (locale: "th" | "en") => ["fuel", "bangchak", locale] as const,
    },
    expenses: <P extends Params>(params: P) => ["expenses", params] as const,
    statements: {
        all: () => ["statements"] as const,
        list: <P extends Params>(params: P) => ["statements", params] as const,
        detail: (id: string) => ["statements", "detail", id] as const,
    },
    maintenance: <P extends Params>(params?: P) => listKey("maintenance", params),
    payroll: <P extends Params>(params?: P) => listKey("payroll", params),
    penalties: <P extends Params>(params?: P) => listKey("penalties", params),
    leave: <P extends Params>(params?: P) => listKey("leave", params),

    chats: {
        queued: () => ["chats", "queued"] as const,
        mine: () => ["chats", "mine"] as const,
    },
    chat: {
        detail: (id: string) => ["chat", id] as const,
        messages: (id: string) => ["chat", id, "messages"] as const,
    },
    broadcasts: () => ["broadcasts"] as const,

    security: {
        overview: () => ["security", "overview"] as const,
        usersByRole: () => ["security", "usersByRole"] as const,
        events: <P extends Params>(params: P) => ["security", "events", params] as const,
    },
    users: {
        all: () => ["users"] as const,
        list: <P extends Params>(params: P) => ["users", params] as const,
        sessions: (id: string) => ["users", "sessions", id] as const,
    },
    /** The tenants page and pickers of T18 (`GET /v1/tenants`, `/v1/tenants/{id}/members`). */
    tenants: {
        all: () => ["tenants"] as const,
        list: <P extends Params>(params: P) => ["tenants", params] as const,
        members: <P extends Params>(tenantId: string, params: P) => ["tenants", tenantId, "members", params] as const,
    },
    apiKeys: () => ["apiKeys"] as const,
    installations: {
        all: () => ["installations"] as const,
        list: <P extends Params>(params: P) => ["installations", params] as const,
        stats: <P extends Params>(params: P) => ["installations", "stats", params] as const,
    },
    mobileSettings: () => ["mobileSettings"] as const,
    mobileReleases: <P extends Params>(params: P) => ["mobileReleases", params] as const,
    waitlist: () => ["waitlist"] as const,
} as const;
