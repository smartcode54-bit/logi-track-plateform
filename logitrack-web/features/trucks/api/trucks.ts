/**
 * Trucks, renewals and the truck import for the web (T25; developer-spec.md §10.7, §10.10, §10.13;
 * Appendix B §B.2.8 as built by T22; Appendix E §E.2.2, §E.4 row "trucks", §E.5 row 30, §E.6).
 *
 * Keys (`['trucks', …]` and `['renewals', …]`; the `tenant:{tid}:fleet` events invalidate both):
 * - `['trucks', {ownership, status, type, q, tenantId}]` (infinite, 100 per page): the trucks list and
 *   a carrier's trucks, server-filtered keyset pages (`GET /v1/trucks`); no 100-row cap. Polled every
 *   60 s while the tab is visible and refetched on focus once older than that (row 30).
 * - `['trucks','fleetStats']`: every truck, for the fleet and PM counters above the list.
 * - `['trucks','options']`: own-fleet trucks for the truck-assignment picker.
 * - `['trucks','detail',id]`: `GET /v1/trucks/{id}` (status history, current assignments, files).
 * - `['renewals', {}]`: `GET /v1/trucks/renewals` (tax and insurance state per truck, Bangkok calendar).
 *
 * The hooks return the Go DTOs. Go is the only source (TW7 removed the Firestore adapter and the
 * `masterdata` flag switch of T25).
 */
import { infiniteQueryOptions, queryOptions, type QueryClient } from "@tanstack/react-query";
import { apiDate } from "@/lib/calendarDate";
import { goFetch, goPath } from "@/lib/goFetch";
import { fetchAllPages } from "@/lib/goPages";
import { goInfiniteQueryFn, goNextPageParam } from "@/lib/goQuery";
import { uploadToGo, type UploadPurpose } from "@/lib/goUpload";
import { getQueryClient } from "@/lib/queryClient";
import { QUERY_POLICY, queryKeys } from "@/lib/queryKeys";
import type { TruckFormValues } from "@/validate/truckSchema";

export const TRUCKS_PATH = "/v1/trucks";
/** Page size of the trucks list (Appendix E §E.10: the list stopped at 100 rows; now it pages). */
export const TRUCKS_PAGE_SIZE = 100;
/** Bound of a full read (fleet counters, pickers, renewals): 200 pages of 100. */
export const TRUCKS_MAX_PAGES = 200;

export type TruckStatus = "active" | "inactive" | "maintenance" | "insurance_claim" | "sold";
export type Ownership = "own" | "subcontractor";
export type RenewalStatus = "pending" | "in_progress" | "completed";
export type PaymentMethod = "cash" | "transfer" | "company_credit";

/** A truck as `GET /v1/trucks` lists it (internal/fleet `Truck`); dates `yyyy-MM-dd`. */
export interface TruckDTO {
    id: string;
    /** The truck's tenant: the own fleet, or the carrier (= subcontractor id). */
    tenantId: string;
    ownershipType: Ownership;
    licensePlate: string;
    province: string;
    vin: string | null;
    engineNumber: string | null;
    gpsVehicleId: string | null;
    status: TruckStatus | string;
    /** The raw legacy status of a loaded truck (`Available`), cleared by a status change. */
    legacyStatus: string | null;
    brand: string;
    model: string;
    year: number;
    color: string;
    type: string;
    vehicleClass: string | null;
    seats: number | null;
    fuelType: string | null;
    engineCapacity: number | null;
    fuelCapacity: number | null;
    maxLoadWeightKg: number | null;
    registrationDate: string | null;
    buyingDate: string | null;
    notes: string | null;
    taxExpiryDate: string | null;
    /** Cost fields: null without fleet:view_renewals / manage_renewals / edit_truck. */
    taxExpenseThb: number | null;
    taxResponsible: string;
    taxRenewalStatus: RenewalStatus | null;
    maintenanceResponsible: string;
    lastServiceDate: string | null;
    nextServiceDate: string | null;
    nextServiceMileage: number | null;
    pmIntervalKm: number | null;
    currentMileage: number | null;
    insurancePolicyId: string | null;
    insurancePolicyNumber: string | null;
    insuranceCompany: string | null;
    insuranceType: string | null;
    insuranceStartDate: string | null;
    insuranceExpiryDate: string | null;
    insurancePremiumThb: number | null;
    insuranceNotes: string | null;
    insuranceRenewalStatus: RenewalStatus | null;
    paymentMethod: PaymentMethod | null;
    createdAt: string | null;
    updatedAt: string | null;
}

export interface TruckStatusEntry {
    status: string;
    previousStatus: string | null;
    changedAt: string | null;
    changedBy: string | null;
    changedByLabel: string | null;
    reason: string | null;
}

/** An active assignment of the truck (`truck_assignments`, R14). */
export interface TruckCurrentAssignment {
    id: string;
    driverId: string;
    driverName: string | null;
    assignedAt: string | null;
}

export interface TruckFile {
    key: string;
    url: string | null;
}

/** File kinds of `files` (single-file kinds hold one entry). */
export type TruckFileKind =
    | "imageFrontRight"
    | "imageFrontLeft"
    | "imageBackRight"
    | "imageBackLeft"
    | "documentTax"
    | "documentRegister"
    | "taxReceipt"
    | "insuranceReceipt"
    | "insuranceDocument"
    | "legacyImage"
    | "other";

/** `GET /v1/trucks/{id}` and the answer of every truck write. */
export interface TruckDetailDTO extends TruckDTO {
    statusHistory: TruckStatusEntry[];
    currentAssignments: TruckCurrentAssignment[];
    files: Partial<Record<TruckFileKind, TruckFile[]>>;
}

/** One side of a renewal (`GET /v1/trucks/renewals`). */
export interface RenewalState {
    expiryDate: string | null;
    renewalStatus: RenewalStatus | null;
    state: "in_progress" | "overdue" | "expiring_soon" | "ok";
    daysRemaining: number | null;
    amountThb: number | null;
}

export interface RenewalDTO {
    truckId: string;
    tenantId: string;
    ownershipType: Ownership;
    licensePlate: string;
    province: string;
    status: string;
    type: string;
    vehicleClass: string | null;
    paymentMethod: PaymentMethod | null;
    nextDueDate: string | null;
    tax: RenewalState & { responsible: string };
    insurance: RenewalState & { company: string | null; policyNumber: string | null; coverage: string | null };
}

/** A truck option of the truck-assignment picker. */
export interface TruckOption {
    id: string;
    plate: string;
    model: string;
    type: string;
    status: string;
}

export { apiDate, localDate } from "@/lib/calendarDate";

// --- display helpers ---------------------------------------------------------------------------

/** The truck form's status value of an API status (the form keeps `insurance-claim`). */
const FORM_STATUS: Record<string, TruckFormValues["truckStatus"]> = {
    active: "active",
    inactive: "inactive",
    maintenance: "maintenance",
    insurance_claim: "insurance-claim",
    sold: "sold",
};
const FORM_PAYMENT: Record<PaymentMethod, "Cash" | "Transfer" | "Company Credit"> = {
    cash: "Cash",
    transfer: "Transfer",
    company_credit: "Company Credit",
};
const API_PAYMENT: Record<string, PaymentMethod> = { Cash: "cash", Transfer: "transfer", "Company Credit": "company_credit" };

/** The API status of a legacy or form status (`insurance-claim`, `Available`, `Active`). */
export function apiTruckStatus(raw: string | null | undefined): TruckStatus | null {
    const s = String(raw ?? "").trim().toLowerCase().replace(/[- ]/g, "_");
    if (s === "available") return "active";
    return (["active", "inactive", "maintenance", "insurance_claim", "sold"] as const).find((v) => v === s) ?? null;
}

/** The API payment method of a legacy or form value. */
export function apiPaymentMethod(raw: string | null | undefined): PaymentMethod | null {
    if (!raw) return null;
    if (raw in API_PAYMENT) return API_PAYMENT[raw];
    const s = raw.trim().toLowerCase().replace(/ /g, "_");
    return (["cash", "transfer", "company_credit"] as const).find((v) => v === s) ?? null;
}

/** The form's (legacy) payment label of an API value. */
export function formPaymentMethod(v: PaymentMethod | null | undefined): "Cash" | "Transfer" | "Company Credit" | "" {
    return v ? FORM_PAYMENT[v] : "";
}

const num = (v: unknown): number | null => {
    if (v === undefined || v === null || v === "") return null;
    const n = typeof v === "number" ? v : Number(v);
    return Number.isFinite(n) ? n : null;
};

const text = (v: unknown): string | null => {
    const s = String(v ?? "").trim();
    return s === "" ? null : s;
};

/** The URL of the first file of a kind ("" without one, or when it has no URL). For display only. */
export function fileUrl(files: TruckDetailDTO["files"], kind: TruckFileKind): string {
    return files[kind]?.[0]?.url ?? "";
}

/**
 * The ref of the first file of a kind ("" without one): its key. Go lists every linked file with its
 * key and a URL that may be null (`missing_at_source` after the ETL, an unsignable object), so the
 * forms hold keys, never URLs: a file without a URL is still a file the save must keep (T25 review).
 * On Firestore the key is the URL.
 */
export function fileRef(files: TruckDetailDTO["files"], kind: TruckFileKind): string {
    return files[kind]?.[0]?.key ?? "";
}

/** The refs (keys) of a stored truck's files, per kind: what a save compares the form against. */
export function storedFileKeys(files: TruckDetailDTO["files"]): Partial<Record<TruckFileKind, string[]>> {
    const out: Partial<Record<TruckFileKind, string[]>> = {};
    for (const [kind, list] of Object.entries(files) as Array<[TruckFileKind, TruckFile[] | undefined]>) {
        out[kind] = (list ?? []).map((f) => f.key).filter(Boolean);
    }
    return out;
}

/** Preview URL of each stored file ref; null when the file has no readable object (shown as unavailable). */
export function truckFilePreviewUrls(files: TruckDetailDTO["files"]): Record<string, string | null> {
    const out: Record<string, string | null> = {};
    for (const list of Object.values(files)) for (const f of list ?? []) if (f.key) out[f.key] = f.url ?? null;
    return out;
}

/**
 * The preview of a form value: a picked file's `blob:` URL is itself, a stored ref resolves through
 * `previews` (null: unavailable), anything else (a Firestore URL) is shown as it is.
 */
export function previewOfRef(previews: Readonly<Record<string, string | null>>, ref: string): string | null {
    if (!ref || ref.startsWith("blob:")) return ref || null;
    return ref in previews ? previews[ref] : ref;
}

/** The truck form's values for a stored truck (`EditTruckForm`); file fields hold refs (`fileRef`). */
export function truckFormValues(t: TruckDetailDTO): Partial<TruckFormValues> {
    return {
        ownershipType: t.ownershipType,
        subcontractorId: t.ownershipType === "subcontractor" ? t.tenantId : "",
        licensePlate: t.licensePlate,
        province: t.province,
        vin: t.vin ?? "",
        engineNumber: t.engineNumber ?? "",
        GPSVehicleId: t.gpsVehicleId ?? "",
        truckStatus: FORM_STATUS[String(t.status)] ?? "",
        brand: t.brand,
        model: t.model,
        year: t.year ? String(t.year) : "",
        color: t.color,
        type: t.type,
        seats: t.seats === null ? "" : String(t.seats),
        fuelType: t.fuelType ?? "",
        engineCapacity: t.engineCapacity ?? undefined,
        fuelCapacity: t.fuelCapacity ?? undefined,
        maxLoadWeight: t.maxLoadWeightKg ?? undefined,
        registrationDate: t.registrationDate ?? undefined,
        buyingDate: t.buyingDate ?? undefined,
        notes: t.notes ?? "",
        imageFrontRight: fileRef(t.files, "imageFrontRight"),
        imageFrontLeft: fileRef(t.files, "imageFrontLeft"),
        imageBackRight: fileRef(t.files, "imageBackRight"),
        imageBackLeft: fileRef(t.files, "imageBackLeft"),
        documentTax: fileRef(t.files, "documentTax"),
        documentRegister: fileRef(t.files, "documentRegister"),
        taxExpiryDate: t.taxExpiryDate ?? undefined,
        lastServiceDate: t.lastServiceDate ?? undefined,
        nextServiceDate: t.nextServiceDate ?? undefined,
        nextServiceMileage: t.nextServiceMileage ?? undefined,
        pmIntervalKm: t.pmIntervalKm ?? undefined,
        currentMileage: t.currentMileage ?? undefined,
        insurancePolicyId: t.insurancePolicyId ?? "",
        insurancePolicyNumber: t.insurancePolicyNumber ?? "",
        insuranceCompany: t.insuranceCompany ?? "",
        insuranceType: (t.insuranceType ?? "") as TruckFormValues["insuranceType"],
        insuranceStartDate: t.insuranceStartDate ?? undefined,
        insuranceExpiryDate: t.insuranceExpiryDate ?? undefined,
        insurancePremium: t.insurancePremiumThb ?? undefined,
        insuranceDocuments: (t.files.insuranceDocument ?? []).map((f) => f.key).filter(Boolean),
        insuranceNotes: t.insuranceNotes ?? "",
        taxResponsible: t.taxResponsible,
        maintenanceResponsible: t.maintenanceResponsible,
        taxRenewalStatus: t.taxRenewalStatus ?? undefined,
        insuranceRenewalStatus: t.insuranceRenewalStatus ?? undefined,
        taxExpense: t.taxExpenseThb ?? undefined,
        paymentMethod: formPaymentMethod(t.paymentMethod),
    };
}

/**
 * The Go body of the truck form, without files (`POST /v1/trucks`, `PATCH /v1/trucks/{id}`). The
 * renewal fields stay out: they change through `POST /v1/trucks/{id}/renewals` (the edit form never
 * wrote them either).
 */
export function truckBodyFromForm(v: TruckFormValues): Record<string, unknown> {
    return {
        licensePlate: v.licensePlate.trim(),
        province: v.province.trim(),
        vin: text(v.vin),
        engineNumber: text(v.engineNumber),
        gpsVehicleId: text(v.GPSVehicleId),
        status: apiTruckStatus(v.truckStatus),
        brand: v.brand.trim(),
        model: v.model.trim(),
        year: num(v.year),
        color: v.color.trim(),
        type: v.type.trim(),
        seats: num(v.seats),
        fuelType: text(v.fuelType),
        engineCapacity: num(v.engineCapacity),
        fuelCapacity: num(v.fuelCapacity),
        maxLoadWeightKg: num(v.maxLoadWeight),
        registrationDate: apiDate(v.registrationDate),
        buyingDate: apiDate(v.buyingDate),
        notes: text(v.notes),
        taxExpiryDate: apiDate(v.taxExpiryDate),
        taxResponsible: text(v.taxResponsible),
        maintenanceResponsible: text(v.maintenanceResponsible),
        lastServiceDate: apiDate(v.lastServiceDate),
        nextServiceDate: apiDate(v.nextServiceDate),
        nextServiceMileage: num(v.nextServiceMileage),
        pmIntervalKm: num(v.pmIntervalKm),
        currentMileage: num(v.currentMileage),
        insurancePolicyId: text(v.insurancePolicyId),
        insurancePolicyNumber: text(v.insurancePolicyNumber),
        insuranceCompany: text(v.insuranceCompany),
        insuranceType: text(v.insuranceType),
        insuranceStartDate: apiDate(v.insuranceStartDate),
        insuranceExpiryDate: apiDate(v.insuranceExpiryDate),
        insurancePremiumThb: num(v.insurancePremium),
        insuranceNotes: text(v.insuranceNotes),
    };
}

/** API field -> truck form field (422 / 409 details of a truck write, T25). */
export const TRUCK_FIELD_MAP = {
    status: "truckStatus",
    gpsVehicleId: "GPSVehicleId",
    maxLoadWeightKg: "maxLoadWeight",
    insurancePremiumThb: "insurancePremium",
    tenantId: "subcontractorId",
} as const;

// --- reads -------------------------------------------------------------------------------------

export interface TruckListFilter {
    ownership?: "" | Ownership;
    status?: string;
    type?: string;
    q?: string;
    /** A carrier's trucks (the subcontractor page). */
    tenantId?: string;
}

/** The key params: only set filters, so equal filters share one entry. */
export function truckListParams(f: TruckListFilter) {
    return {
        ownership: (f.ownership || "") as "" | Ownership,
        status: f.status || "",
        type: f.type || "",
        q: (f.q ?? "").trim(),
        tenantId: f.tenantId || "",
    };
}

type TruckListKey = ReturnType<typeof queryKeys.trucks.list<ReturnType<typeof truckListParams>>>;

/** `['trucks', filters]` (infinite): `GET /v1/trucks?ownership&tenantId&status&type&q&cursor`. */
export function truckPagesQuery(filter: TruckListFilter) {
    return infiniteQueryOptions({
        queryKey: queryKeys.trucks.list(truckListParams(filter)),
        queryFn: goInfiniteQueryFn<TruckDTO, TruckListKey>(TRUCKS_PATH, {
            query: ([, p]) => ({
                ownership: p.ownership || undefined,
                status: p.status || undefined,
                type: p.type || undefined,
                q: p.q || undefined,
                tenantId: p.tenantId || undefined,
                limit: TRUCKS_PAGE_SIZE,
            }),
        }),
        initialPageParam: undefined as string | undefined,
        getNextPageParam: goNextPageParam,
        ...QUERY_POLICY.entity,
    });
}

/** `['trucks','fleetStats']`: every truck, for the counters above the list. */
export const fleetStatsQuery = queryOptions({
    queryKey: queryKeys.trucks.fleetStats(),
    queryFn: ({ signal }) => fetchAllPages<TruckDTO>(TRUCKS_PATH, {}, { signal, maxPages: TRUCKS_MAX_PAGES }),
    ...QUERY_POLICY.entity,
});

/**
 * `['trucks','options']`: own-fleet trucks not in maintenance, for the truck-assignment page's "total trucks"
 * counter (its picker searches on the server since UI2C; `GET /v1/trucks` has no count to read instead).
 */
export const truckOptionsQuery = queryOptions({
    queryKey: queryKeys.trucks.options(),
    queryFn: async ({ signal }) =>
        toOptions(await fetchAllPages<TruckDTO>(TRUCKS_PATH, { ownership: "own" }, { signal, maxPages: TRUCKS_MAX_PAGES })),
    ...QUERY_POLICY.entity,
});

function toOptions(trucks: TruckDTO[]): TruckOption[] {
    return trucks
        .filter((t) => t.status !== "maintenance")
        .map((t) => ({ id: t.id, plate: t.licensePlate, model: t.model, type: t.type, status: String(t.status) }));
}

/** `['trucks','detail',id]`. */
export function truckQuery(id: string) {
    return queryOptions({
        queryKey: queryKeys.trucks.detail(id),
        queryFn: ({ signal }) => goFetch<TruckDetailDTO>(goPath`/v1/trucks/${id}`, { signal }),
        ...QUERY_POLICY.entity,
        enabled: id !== "",
    });
}

/** `['renewals', {}]`: every truck's tax and insurance state (the renewals page). */
export const renewalsQuery = queryOptions({
    queryKey: queryKeys.renewals({}),
    queryFn: ({ signal }) => fetchAllPages<RenewalDTO>(`${TRUCKS_PATH}/renewals`, {}, { signal, maxPages: TRUCKS_MAX_PAGES }),
    ...QUERY_POLICY.entity,
});

export function invalidateFleet(client: QueryClient = getQueryClient()): Promise<void> {
    return Promise.all([
        client.invalidateQueries({ queryKey: queryKeys.trucks.all() }),
        client.invalidateQueries({ queryKey: queryKeys.renewals() }),
    ]).then(() => undefined);
}

// --- writes ------------------------------------------------------------------------------------

/** Files picked in the form, keyed by the `blob:` URL the form holds until the save. */
export type PendingFiles = ReadonlyMap<string, File>;

export interface TruckSave {
    values: TruckFormValues;
    pending: PendingFiles;
    /** The loaded truck's file refs per kind (`storedFileKeys`); absent on a create. */
    stored?: Partial<Record<TruckFileKind, string[]>>;
    /** Legacy (the actor the retired Firestore path recorded): Go takes the actor from the session. */
    userId?: string;
}

const SINGLE_FILE_KINDS: ReadonlyArray<{ field: keyof TruckFormValues; kind: TruckFileKind; purpose: UploadPurpose }> = [
    { field: "imageFrontRight", kind: "imageFrontRight", purpose: "truck_photo" },
    { field: "imageFrontLeft", kind: "imageFrontLeft", purpose: "truck_photo" },
    { field: "imageBackRight", kind: "imageBackRight", purpose: "truck_photo" },
    { field: "imageBackLeft", kind: "imageBackLeft", purpose: "truck_photo" },
    { field: "documentTax", kind: "documentTax", purpose: "truck_document" },
    { field: "documentRegister", kind: "documentRegister", purpose: "truck_document" },
];

const sameList = (a: readonly string[], b: readonly string[]) => a.length === b.length && a.every((v, i) => v === b[i]);

/**
 * The `files` body of a Go write. Only the kinds the user changed go out (Go replaces a kind that is
 * present, and `[]` unlinks its files): a picked file is uploaded (the key embeds the truck), a
 * stored one goes back as its key, a cleared slot is `[]`. A kind equal to what was loaded, a kind the
 * form does not edit, and a kind holding a value the API does not know are left out, so a save never
 * unlinks a file the user did not remove (a file without a URL included, T25 review).
 */
export async function truckFilesBody(truckId: string, save: TruckSave): Promise<Record<string, string[]>> {
    const stored = save.stored ?? {};
    const known = new Set(Object.values(stored).flatMap((keys) => keys ?? []));
    const refOf = async (value: string, purpose: UploadPurpose): Promise<string | null | undefined> => {
        if (!value) return null;
        if (value.startsWith("blob:")) {
            const file = save.pending.get(value);
            return file ? uploadToGo(file, { purpose, entityId: truckId }) : undefined;
        }
        return known.has(value) ? value : undefined; // undefined: a ref the API does not know (leave the kind alone)
    };
    const files: Record<string, string[]> = {};
    for (const { field, kind, purpose } of SINGLE_FILE_KINDS) {
        const ref = await refOf(String(save.values[field] ?? ""), purpose);
        if (ref === undefined) continue;
        const next = ref ? [ref] : [];
        if (!sameList(next, stored[kind] ?? [])) files[kind] = next;
    }
    const docs: string[] = [];
    let complete = true;
    for (const value of save.values.insuranceDocuments ?? []) {
        const ref = await refOf(value, "insurance_document");
        if (ref === undefined) complete = false;
        else if (ref) docs.push(ref);
    }
    if (complete && !sameList(docs, stored.insuranceDocument ?? [])) files.insuranceDocument = docs;
    return files;
}

async function createOnGo(save: TruckSave): Promise<string> {
    const body: Record<string, unknown> = { ...truckBodyFromForm(save.values) };
    if (save.values.ownershipType === "subcontractor" && save.values.subcontractorId) body.tenantId = save.values.subcontractorId;
    const created = await goFetch<TruckDetailDTO>(TRUCKS_PATH, { method: "POST", body });
    const files = await truckFilesBody(created.id, save);
    if (Object.values(files).some((list) => list.length > 0)) {
        await goFetch(goPath`/v1/trucks/${created.id}`, { method: "PATCH", body: { files } });
    }
    return created.id;
}

async function updateOnGo(id: string, save: TruckSave): Promise<void> {
    const files = await truckFilesBody(id, save);
    const body: Record<string, unknown> = truckBodyFromForm(save.values);
    if (Object.keys(files).length > 0) body.files = files;
    await goFetch(goPath`/v1/trucks/${id}`, { method: "PATCH", body });
}

/** Registers a truck (`409 already_exists` on a taken plate); returns its id. */
export const createTruck = (save: TruckSave): Promise<string> => createOnGo(save);

export const updateTruck = ({ id, ...save }: TruckSave & { id: string }): Promise<void> => updateOnGo(id, save);

/** `POST /v1/trucks/{id}/renewals` (RenewalForm): one renewal, saved or completed. */
export interface RenewalSave {
    truck: TruckDetailDTO;
    type: "tax" | "insurance";
    status: RenewalStatus;
    amount: number | null;
    paymentMethod: string;
    expiryDate: string;
    responsible: string;
    receipt: File | null;
    /** Insurance only. */
    startDate?: string;
    provider?: string;
    policyId?: string;
    policyNumber?: string;
    coverage?: string;
    /** Insurance only: the truck's insurance notes (never the ledger row's note, which stays the legacy text). */
    insuranceNotes?: string;
    /** Insurance documents: the stored refs (keys) kept and the new files. */
    documents?: string[];
    newDocuments?: File[];
    /** Legacy (the actor the retired Firestore path recorded): Go takes the actor from the session. */
    actorLabel?: string;
    userId?: string;
}

/**
 * The Go renewal. `notes` (the ledger row's note) is never sent: the form has no such input, so Go
 * writes the legacy "Expiry: …" / "Policy: … | Expiry: …" text as the retired Firestore path did. The
 * insurance form's own fields go where the legacy form wrote them (T25 review): its notes are the
 * truck's `insuranceNotes`, its person in charge `responsible` (Go stores it as
 * `maintenanceResponsible` on an insurance renewal), and its document list, when it changed, is
 * `documents` (the stored keys kept plus the new uploads; a removed stored document is unlinked).
 */
async function renewOnGo(r: RenewalSave): Promise<void> {
    const body: Record<string, unknown> = {
        type: r.type,
        status: r.status,
        amount: r.amount,
        paymentMethod: apiPaymentMethod(r.paymentMethod),
        expiryDate: apiDate(r.expiryDate),
        responsible: text(r.responsible),
    };
    if (r.receipt) body.receiptKey = await uploadToGo(r.receipt, { purpose: "truck_receipt", entityId: r.truck.id });
    if (r.type === "insurance") {
        body.startDate = apiDate(r.startDate);
        body.provider = text(r.provider);
        body.policyId = text(r.policyId);
        body.policyNumber = text(r.policyNumber);
        body.coverage = text(r.coverage);
        body.insuranceNotes = text(r.insuranceNotes);
        const stored = (r.truck.files.insuranceDocument ?? []).map((f) => f.key);
        const kept = (r.documents ?? []).filter((k) => stored.includes(k));
        const added: string[] = [];
        for (const file of r.newDocuments ?? []) added.push(await uploadToGo(file, { purpose: "insurance_document", entityId: r.truck.id }));
        if (added.length > 0 || !sameList(kept, stored)) body.documents = [...kept, ...added];
    }
    await goFetch(goPath`/v1/trucks/${r.truck.id}/renewals`, { method: "POST", body });
}

export const renewTruck = (r: RenewalSave): Promise<void> => renewOnGo(r);

/** The answer of an import (Go `POST /v1/trucks/import`). */
export interface TruckImportResult {
    created: number;
    updated: number;
    unchanged: number;
    errors: Array<{ row: number; licensePlate: string | null; fields: Array<{ field: string; reason: string }> }>;
}

/**
 * Imports the parsed sheet: Go takes the rows as they are (headers of the legacy template, the API
 * names or Thai; it validates and upserts by plate). `validRows` (what the dialog validated for the
 * retired Firestore path) is not sent.
 */
export function importTrucks({ rawRows }: { rawRows: Record<string, unknown>[]; validRows: Record<string, unknown>[] }): Promise<TruckImportResult> {
    return goFetch<TruckImportResult>(`${TRUCKS_PATH}/import`, { method: "POST", body: { rows: rawRows } });
}
