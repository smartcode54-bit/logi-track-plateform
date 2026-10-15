import { Loader2 } from "lucide-react";

/** Query loaders already have a translator in their parent; keep this boundary presentational. */
export function TruckQueryLoading({ label }: { label: string }) {
    return (
        <div className="flex min-h-[50vh] items-center justify-center" role="status" aria-busy="true">
            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" aria-hidden />
            <span className="sr-only">{label}</span>
        </div>
    );
}
