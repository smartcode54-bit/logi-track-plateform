"use client";

import dynamic from "next/dynamic";

const EditDriverClient = dynamic(() => import("./EditDriverClient"), {
    ssr: false,
    loading: () => (
        <div className="flex items-center justify-center min-h-[400px]">
            <span className="text-muted-foreground">Loading...</span>
        </div>
    ),
});

export default function EditDriverWrapper() {
    return <EditDriverClient />;
}
