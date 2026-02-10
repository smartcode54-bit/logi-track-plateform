"use client";

import dynamic from "next/dynamic";

// Dynamic import with SSR disabled - required for static export with dynamic routes
// The component fetches data from Firebase at runtime, not build time
const EditTruckClient = dynamic(() => import("./EditTruckClient"), {
    ssr: false,
    loading: () => (
        <div className="flex items-center justify-center min-h-[400px]">
            <span className="text-muted-foreground">Loading...</span>
        </div>
    ),
});

export default function EditTruckWrapper() {
    return <EditTruckClient />;
}
