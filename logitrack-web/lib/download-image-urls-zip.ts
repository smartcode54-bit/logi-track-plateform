import JSZip from "jszip";
import { format } from "date-fns";
import { looksLikeImageUrl } from "@/features/maintenance/utils/looksLikeImageUrl";

/** Logical name without extension; final name is stem + ext from blob/URL. */
export type ZipImageEntryInput = {
    url: string;
    filenameStem: string;
};

export type ZipImageDownloadResult = {
    added: string[];
    failed: { url: string; reason: string }[];
};

export type ZipBatchExpenseLike = {
    id: string;
    date: Date;
    receiptPhotoUrl?: string | null;
    odometerPhotoUrl?: string | null;
};

/** Parallel fetches per tick (batch); avoids one-by-one sequential downloads. */
export const DEFAULT_ZIP_FETCH_CONCURRENCY = 8;

function extFromBlobAndUrl(blob: Blob, url: string): string {
    const mime = blob.type?.toLowerCase() ?? "";
    if (mime.includes("png")) return ".png";
    if (mime.includes("webp")) return ".webp";
    if (mime.includes("gif")) return ".gif";
    if (mime.includes("jpeg") || mime.includes("jpg")) return ".jpg";
    const m = url.match(/\.(jpe?g|png|gif|webp)(\?|#|$)/i);
    if (m) {
        const e = m[1].toLowerCase();
        return e === "jpeg" ? ".jpg" : `.${e}`;
    }
    return ".jpg";
}

function triggerBlobDownload(blob: Blob, filename: string): void {
    const u = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = u;
    a.download = filename;
    a.rel = "noopener";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(u);
}

type FetchOneResult =
    | { ok: true; name: string; blob: Blob }
    | { ok: false; url: string; reason: string };

async function fetchOneEntry(entry: ZipImageEntryInput): Promise<FetchOneResult> {
    const { url, filenameStem } = entry;
    try {
        const res = await fetch(url, { mode: "cors", credentials: "omit" });
        if (!res.ok) {
            return { ok: false, url, reason: `HTTP ${res.status}` };
        }
        const blob = await res.blob();
        const ext = extFromBlobAndUrl(blob, url);
        const name = `${filenameStem}${ext}`;
        return { ok: true, name, blob };
    } catch (e) {
        const reason = e instanceof Error ? e.message : String(e);
        return { ok: false, url, reason };
    }
}

function isOkFetch(r: FetchOneResult): r is { ok: true; name: string; blob: Blob } {
    return r.ok === true;
}

/**
 * Fetch images in parallel batches (default concurrency 8), build ZIP, trigger download.
 * Only calls generateAsync / download when at least one file was added.
 */
export async function downloadImagesAsZip(
    zipFilename: string,
    entries: ZipImageEntryInput[],
    options?: { fetchConcurrency?: number }
): Promise<ZipImageDownloadResult> {
    const zip = new JSZip();
    const added: string[] = [];
    const failed: { url: string; reason: string }[] = [];
    const concurrency = Math.max(
        1,
        Math.min(32, options?.fetchConcurrency ?? DEFAULT_ZIP_FETCH_CONCURRENCY)
    );

    for (let i = 0; i < entries.length; i += concurrency) {
        const slice = entries.slice(i, i + concurrency);
        const results = await Promise.all(slice.map((e) => fetchOneEntry(e)));
        for (const r of results) {
            if (isOkFetch(r)) {
                zip.file(r.name, r.blob);
                added.push(r.name);
            } else {
                failed.push({ url: r.url, reason: r.reason });
            }
        }
    }

    if (added.length === 0) {
        return { added, failed };
    }

    const out = await zip.generateAsync({ type: "blob" });
    triggerBlobDownload(out, zipFilename);
    return { added, failed };
}

/** Single-row ZIP (e.g. audit dialog). Odometer optional. */
export function buildAccountingZipEntries(
    expenseId: string,
    receiptPhotoUrl?: string | null,
    odometerPhotoUrl?: string | null
): ZipImageEntryInput[] {
    const safeId = expenseId.replace(/[/\\]/g, "_");
    const out: ZipImageEntryInput[] = [];
    const r = receiptPhotoUrl?.trim();
    const o = odometerPhotoUrl?.trim();
    if (r && looksLikeImageUrl(r)) {
        out.push({ url: r, filenameStem: `${safeId}_receipt` });
    }
    if (o && looksLikeImageUrl(o)) {
        out.push({ url: o, filenameStem: `${safeId}_odometer` });
    }
    return out;
}

/** Batch card: receipts only, no odometer. Filenames: `yyyyMMdd_{id}_receipt` + ext. */
export function buildBatchZipEntriesFromRows(rows: ZipBatchExpenseLike[]): ZipImageEntryInput[] {
    const out: ZipImageEntryInput[] = [];
    for (const row of rows) {
        const safeId = row.id.replace(/[/\\]/g, "_");
        const dateStr = format(row.date, "yyyyMMdd");
        const r = row.receiptPhotoUrl?.trim();
        if (r && looksLikeImageUrl(r)) {
            out.push({ url: r, filenameStem: `${dateStr}_${safeId}_receipt` });
        }
    }
    return out;
}

/** Heuristic for surfacing Storage CORS guidance in UI toasts. */
export function shouldSuggestCorsHint(failed: { reason: string }[]): boolean {
    return failed.some((f) =>
        /failed to fetch|networkerror|load failed|cors|aborted/i.test(f.reason)
    );
}

export type TripPhotoZipInput = {
    url: string;
    type: string;
};

/** ZIP entries for trip evidence photos; index suffix avoids filename clashes when types repeat. */
export function buildTripPhotosZipEntries(
    tripId: string,
    photos: TripPhotoZipInput[]
): ZipImageEntryInput[] {
    const safeTripId = tripId.replace(/[/\\]/g, "_");
    const out: ZipImageEntryInput[] = [];
    photos.forEach((p, i) => {
        const u = p.url?.trim();
        if (!u || !looksLikeImageUrl(u)) return;
        const safeType = String(p.type).replace(/[/\\]/g, "_");
        out.push({ url: u, filenameStem: `${safeTripId}_${safeType}_${i}` });
    });
    return out;
}

/** Fetch one image URL and trigger browser download (same fetch path as ZIP entries). */
export async function downloadSingleImageUrl(
    url: string,
    filenameStem: string
): Promise<{ ok: true; filename: string } | { ok: false; reason: string }> {
    const r = await fetchOneEntry({ url, filenameStem });
    if (!isOkFetch(r)) {
        return { ok: false, reason: r.reason };
    }
    triggerBlobDownload(r.blob, r.name);
    return { ok: true, filename: r.name };
}
