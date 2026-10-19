"use client";

/** Shared by shell actions that navigate without clicking a link. No draft or permission data is persisted. */
const guards = new Set<() => boolean>();

export function registerNavigationGuard(guard: () => boolean): () => void {
    guards.add(guard);
    return () => { guards.delete(guard); };
}

export function confirmNavigation(): boolean {
    for (const guard of guards) if (!guard()) return false;
    return true;
}
