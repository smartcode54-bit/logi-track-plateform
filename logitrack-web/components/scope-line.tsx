"use client";

/**
 * The scope line as text (UI3B): "slice · role", where only the slice is shortened when space runs out, never the
 * role; the compact header may wrap the role while the slice keeps a visible shortened portion. Line height is normal,
 * so Thai marks above and below the line (ผู้ดูแล…) are not clipped by the truncation's overflow.
 *
 * The same words as `useScopeLabel` (one label source, `principalScope`), which names the scope in accessible
 * names and tooltips.
 */
import { usePrincipalScope } from "@/features/auth/hooks/useScopeLabel";
import { cn } from "@/lib/utils";

export function ScopeLine({ className, testId, compact = false }: { className?: string; testId?: string; compact?: boolean }) {
    const scope = usePrincipalScope();
    if (!scope || (!scope.slice && !scope.role)) return null;
    return (
        // leading-normal last: a caller's text size (text-xs) would otherwise bring its tighter line height back.
        <span className={cn("flex min-w-0", className, "leading-normal")} data-testid={testId}>
            {scope.slice ? <span className={cn("truncate", compact ? "min-w-6 flex-1" : "min-w-0")}>{scope.slice}</span> : null}
            {scope.slice && scope.role ? <span className="shrink-0 whitespace-pre"> · </span> : null}
            {scope.role ? <span className={compact ? "min-w-0 break-words" : "shrink-0 whitespace-nowrap"}>{scope.role}</span> : null}
        </span>
    );
}
