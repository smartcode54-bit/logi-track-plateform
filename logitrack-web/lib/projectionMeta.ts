import type { ProjectionMeta, RedactedField, RedactionKind } from "shared-docs/schemas/supportAccess";

import { ApiError } from "./apiError";

// Enumerable JSON context survives TanStack structural sharing, pagination and persisted caches.
// Only affected objects carry it; paths are relative to that object, never another page's indices.
const CONTEXT = "__redactedFields";
const KINDS = new Set<RedactionKind>(["person", "money", "address", "file", "free_text", "secret"]);

function record(value: unknown): value is Record<string, unknown> {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function redactedFields(value: unknown): readonly RedactedField[] {
    return typeof value === "object" && value !== null && CONTEXT in value && Array.isArray(value[CONTEXT]) ? value[CONTEXT] as RedactedField[] : [];
}

export function fieldIsRedacted(source: unknown, path: string): boolean {
    const pointer = path.startsWith("/") ? path : `/${path}`;
    return redactedFields(source).some((field) => pointer === field.path || pointer.startsWith(`${field.path}/`));
}

/** Validate full-envelope pointers before publishing any projected data. */
export function adaptProjection<T extends { data: unknown; meta?: unknown }>(envelope: T, required = false, requestId = ""): T {
    const meta = envelope.meta;
    const projected = record(meta) && ("redacted" in meta || "supportAccess" in meta);
    if (!required && !projected) return envelope;
    const refuse = (): never => {
        throw new ApiError({ status: 503, code: "support_projection_unavailable", message: "projection metadata unavailable", requestId });
    };
    if (!record(meta) || !Array.isArray(meta.redacted)) return refuse();
    if ("supportAccess" in meta && (!record(meta.supportAccess) || typeof meta.supportAccess.masked !== "boolean" || typeof meta.supportAccess.version !== "string" || !/^\d+$/.test(meta.supportAccess.version))) return refuse();
    const fields: RedactedField[] = [];
    const seen = new Set<string>();
    for (const field of meta.redacted) {
        if (!record(field) || typeof field.path !== "string" || !KINDS.has(field.kind as RedactionKind) || !/^\/(data|meta)(\/|$)/.test(field.path) || /~(?![01])/.test(field.path) || seen.has(field.path)) return refuse();
        seen.add(field.path);
        const segments = field.path.slice(1).split("/").map((s) => s.replaceAll("~1", "/").replaceAll("~0", "~"));
        let value: unknown = envelope;
        for (const segment of segments) {
            if (segment === CONTEXT || segment === "__proto__" || segment === "constructor" || segment === "prototype" || typeof value !== "object" || value === null || !Object.hasOwn(value, segment)) return refuse();
            value = (value as Record<string, unknown>)[segment];
        }
        if (value !== null && value !== "****") return refuse();
        fields.push({ path: field.path, kind: field.kind as RedactionKind });
    }
    const attach = (value: unknown, path: string) => {
        if (typeof value !== "object" || value === null) return;
        const local = fields.filter((f) => f.path === path || f.path.startsWith(`${path}/`)).map((f) => ({ ...f, path: f.path.slice(path.length) }));
        if (Object.hasOwn(value, CONTEXT)) return refuse();
        if (local.length) (value as Record<string, unknown>)[CONTEXT] = local;
        for (const [key, child] of Object.entries(value)) {
            if (key !== CONTEXT) attach(child, `${path}/${key.replaceAll("~", "~0").replaceAll("/", "~1")}`);
        }
    };
    attach(envelope.data, "/data");
    attach(meta, "/meta");
    return envelope;
}

export type { ProjectionMeta };
