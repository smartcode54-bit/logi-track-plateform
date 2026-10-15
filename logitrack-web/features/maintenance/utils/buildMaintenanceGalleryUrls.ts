interface MaintenanceAttachments {
    images?: string[];
    invoices?: string[];
    invoiceUrl?: string | null;
    receipts?: string[];
}

function uniqueUrls(urls: (string | null | undefined)[]): string[] {
    return [...new Set(urls.map((url) => url?.trim()).filter((url): url is string => Boolean(url)))];
}

/** Admin uploads, then every invoice and driver receipt, without duplicate legacy invoice URLs. */
export function buildMaintenanceGalleryUrls(record: MaintenanceAttachments): string[] {
    return uniqueUrls([...(record.images ?? []), ...buildDriverReceiptUrls(record)]);
}

export function buildDriverReceiptUrls(record: MaintenanceAttachments): string[] {
    return uniqueUrls([...(record.invoices ?? []), record.invoiceUrl, ...(record.receipts ?? [])]);
}
