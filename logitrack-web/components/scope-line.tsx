"use client";

/**
 * The scope line as text (UI3B): "slice · role", where only the slice is shortened when space runs out, never the
 * role ("Wanpen-Ratchada (se… · Tenant admin", not "Wanpen-Ratchada (seed) · Tenant ad…"). Line height is normal,
 * so Thai marks above and below the line (ผู้ดูแล…) are not clipped by the truncation's overflow.
 *
 * The same words as `useScopeLabel` (one label source, `principalScope`), which names the scope in accessible
 * names and tooltips.
 */
import { usePrincipalScope } from "@/features/auth/hooks/useScopeLabel";
import { cn } from "@/lib/utils";

export function ScopeLine({ className, testId }: { className?: string; testId?: string }) {
    const scope = usePrincipalScope();
    if (!scope || (!scope.slice && !scope.role)) return null;
    return (
        // leading-normal last: a caller's text size (text-xs) would otherwise bring its tighter line height back.
        <span className={cn("flex min-w-0", className, "leading-normal")} data-testid={testId}>
            {scope.slice ? <span className="min-w-0 truncate">{scope.slice}</span> : null}
            {scope.slice && scope.role ? <span className="shrink-0 whitespace-pre"> · </span> : null}
            {scope.role ? <span className="shrink-0 whitespace-nowrap">{scope.role}</span> : null}
        </span>
    );
}
