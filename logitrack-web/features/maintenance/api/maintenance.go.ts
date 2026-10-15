/**
 * Maintenance records over Go (T42, P3; routes of T40, Appendix B §B.2.15 as built; developer-spec.md
 * §10.7 row `['maintenance', …]`, §10.10; Appendix E §E.2.2).
 *
 * `['maintenance',{truckId,status,type}]` is one keyset list (`GET /v1/maintenance`, newest first, 100
 * per page; the first page carries `meta.statusCounts`): the overview no longer reads every record and
 * every truck (`maintenance.ts:72-104`). Writes go through Go's atomic transitions (`POST`, `PATCH`:
 * one transaction moves the truck's status and `active_maintenance_id`) and invalidate
 * `['maintenance']` and the truck; SSE `maintenance.*` (tenant:{tid}:fleet) does it in other tabs.
 * Files are object keys (`maintenance_file`, variants image / receipt / invoice) read through
 * `GET /v1/files?key=`.
 */
import { infiniteQueryOptions, type QueryClient } from "@tanstack/react-query";
import { goFetch, goPath, goUrl } from "@/lib/goFetch";
import { goInfiniteQueryFn, goNextPageParam, type GoPage } from "@/lib/goQuery";
import { assertUploadable, PartialCreateError, uploadToGo } from "@/lib/goUpload";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";

export const MAINTENANCE_PATH = "/v1/maintenance";
export const MAINTENANCE_PAGE_SIZE = 100;

export const MAINTENANCE_STATUSES = ["pm_booking", "scheduled", "in_progress", "completed", "cancelled"] as const;
export type MaintenanceStatus = (typeof MAINTENANCE_STATUSES)[number];
export type MaintenanceType = "PM" | "CM";
export const PAYMENT_METHODS = ["cash", "credit_card", "billing", "transfer", "insurance_claim"] as const;

export interface MaintenanceFilesDTO {
    images: string[];
    receipts: string[];
    invoices: string[];
}

/** `GET /v1/maintenance` item (internal/maintenance `Record`). */
export interface MaintenanceRecordDTO {
    id: string;
    tenantId: string;
    truckId: string | null;
    licensePlate: string;
    truckBrand: string;
    truckModel: string | null;
    truckOwnership: string | null;
    truckStatus: string | null;
    type: MaintenanceType | string;
    serviceType: string;
    /** `yyyy-MM-dd`. */
    startDate: string;
    endDate: string | null;
    status: MaintenanceStatus | string;
    appointmentTime: string | null;
    pickupAppointmentAt: string | null;
    costLaborThb: number | null;
    costPartsThb: number | null;
    totalCostThb: number | null;
    invoiceAmountThb: number | null;
    /** The cost the pages show and sum (total, else labour + parts, else the driver's invoice amount). */
    displayCostThb: number;
    provider: string | null;
    providerLat: number | null;
    providerLng: number | null;
    paymentMethod: string | null;
    currentMileage: number | null;
    nextServiceMileage: number | null;
    driverSubmitted: boolean;
    checkInAt: string | null;
    checkOutAt: string | null;
    notes: string | null;
    files: MaintenanceFilesDTO;
    createdBy: string | null;
    updatedBy: string | null;
    createdAt: string;
    updatedAt: string;
}

export type MaintenanceFilter = {
    truckId?: string;
    status?: string;
    type?: string;
};

function normalize(f: MaintenanceFilter): MaintenanceFilter {
    const out: MaintenanceFilter = {};
    if (f.truckId) out.truckId = f.truckId;
    if (f.status && f.status !== "all") out.status = f.status;
    if (f.type && f.type !== "all") out.type = f.type;
    return out;
}

type MaintenanceKey = readonly ["maintenance", MaintenanceFilter];

export function maintenanceQuery(filter: MaintenanceFilter, enabled = true) {
    const f = normalize(filter);
    return infiniteQueryOptions({
        queryKey: queryKeys.maintenance(f) as MaintenanceKey,
        queryFn: goInfiniteQueryFn<MaintenanceRecordDTO, MaintenanceKey>(MAINTENANCE_PATH, {
            query: ([, p]) => ({ ...p, limit: MAINTENANCE_PAGE_SIZE }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.list,
        enabled,
    });
}

export function maintenanceRows(pages: GoPage<MaintenanceRecordDTO>[] | undefined): MaintenanceRecordDTO[] {
    return pages?.flatMap((p) => p.data ?? []) ?? [];
}

export function maintenanceStatusCounts(pages: GoPage<MaintenanceRecordDTO>[] | undefined): Partial<Record<string, number>> {
    const meta = pages?.[0]?.meta as { statusCounts?: Record<string, number> } | undefined;
    return meta?.statusCounts ?? {};
}

/** The browser URL of a file key (`GET /v1/files?key=` answers 302 to a signed GET). */
export function maintenanceFileHref(key: string): string {
    return goUrl("/v1/files", { key });
}

/** PM / CM / total of the given records (`displayCostThb`, the server's rule). */
export function maintenanceCostTotals(rows: Pick<MaintenanceRecordDTO, "type" | "displayCostThb">[]) {
    let pm = 0;
    let cm = 0;
    for (const r of rows) {
        if (r.type === "PM") pm += r.displayCostThb || 0;
        else if (r.type === "CM") cm += r.displayCostThb || 0;
    }
    return { pm, cm, total: pm + cm };
}

// --- writes --------------------------------------------------------------------------------------

/** The body of `POST /v1/maintenance` and `PATCH /v1/maintenance/{id}` (absent = unchanged, `null` clears). */
export interface MaintenanceInput {
    truckId?: string;
    type?: MaintenanceType;
    serviceType?: string;
    startDate?: string;
    endDate?: string | null;
    status?: MaintenanceStatus;
    appointmentTime?: string | null;
    pickupAppointmentAt?: string | null;
    costLaborThb?: number | null;
    costPartsThb?: number | null;
    totalCostThb?: number | null;
    provider?: string | null;
    providerLat?: number | null;
    providerLng?: number | null;
    paymentMethod?: string | null;
    currentMileage?: number | null;
    nextServiceMileage?: number | null;
    notes?: string | null;
    files?: Partial<MaintenanceFilesDTO>;
}

export function refreshMaintenance(client: QueryClient = getQueryClient()) {
    return client.invalidateQueries({ queryKey: ["maintenance"] });
}

async function invalidateMaintenance(truckId: string | null | undefined, client: QueryClient) {
    await Promise.all([
        refreshMaintenance(client),
        truckId ? client.invalidateQueries({ queryKey: queryKeys.trucks.detail(truckId) }) : null,
        client.invalidateQueries({ queryKey: ["trucks"], exact: false, refetchType: "none" }),
    ]);
}

/** Uploads new images under the record and returns their keys (`maintenance_file`, variant `image`). */
async function uploadImages(recordId: string, files: File[]): Promise<string[]> {
    assertUploadable(files.map((file) => ({ file, purpose: "maintenance_file" as const })));
    const keys: string[] = [];
    for (const file of files) keys.push(await uploadToGo(file, { purpose: "maintenance_file", entityId: recordId, variant: "image" }));
    return keys;
}

/**
 * Creates a record (one transaction also puts the truck under maintenance), then uploads its images and
 * links them with a `PATCH` (every upload names its entity, developer-spec.md §9.4). A file Go would
 * refuse (JPEG, PNG, WebP or PDF up to 10 MiB) throws `UploadRefusedError` before the `POST`; a failure
 * after the record exists throws `PartialCreateError` with the record (the lists are refreshed first), so
 * the caller retries with `updateMaintenance` on it instead of creating a second record.
 */
export async function createMaintenance(input: MaintenanceInput, images: File[] = [], client: QueryClient = getQueryClient()) {
    assertUploadable(images.map((file) => ({ file, purpose: "maintenance_file" as const })));
    const created = await goFetch<MaintenanceRecordDTO>(MAINTENANCE_PATH, { method: "POST", body: input });
    let out = created;
    try {
        if (images.length > 0) {
            const keys = await uploadImages(created.id, images);
            out = await goFetch<MaintenanceRecordDTO>(goPath`/v1/maintenance/${created.id}`, { method: "PATCH", body: { files: { images: keys } } });
        }
    } catch (err) {
        await invalidateMaintenance(created.truckId, client);
        throw new PartialCreateError(created, err);
    }
    await invalidateMaintenance(out.truckId, client);
    return out;
}

/**
 * Updates a record through a validated transition; new images are added to the record's current
 * ones (a kind named in `files` is replaced as a whole, so the kept keys are sent with the new ones).
 */
export async function updateMaintenance(
    record: Pick<MaintenanceRecordDTO, "id" | "truckId" | "files">,
    input: MaintenanceInput,
    images: File[] = [],
    client: QueryClient = getQueryClient()
) {
    const body: MaintenanceInput = { ...input };
    if (images.length > 0) {
        const keys = await uploadImages(record.id, images);
        body.files = { ...(body.files ?? {}), images: [...(body.files?.images ?? record.files.images ?? []), ...keys] };
    }
    const out = await goFetch<MaintenanceRecordDTO>(goPath`/v1/maintenance/${record.id}`, { method: "PATCH", body });
    await invalidateMaintenance(record.truckId, client);
    return out;
}

/** `POST /v1/maintenance/{id}/remind` -> `202` (FCM `maintenance_scheduled` to the truck's driver). */
export function remindMaintenance(id: string, input: { title?: string; body?: string } = {}) {
    return goFetch<unknown>(goPath`/v1/maintenance/${id}/remind`, { method: "POST", body: input });
}
