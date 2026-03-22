import dynamic from "next/dynamic";

const DriverPreview = dynamic(() => import("@/features/drivers/components/DriverPreview"), {
    ssr: false,
    loading: () => (
        <div className="flex items-center justify-center min-h-[400px]">
            <span className="text-muted-foreground">Loading...</span>
        </div>
    ),
});

export default function DriverViewPage() {
    return <DriverPreview />;
}
