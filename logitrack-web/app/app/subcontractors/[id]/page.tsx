import SubcontractorDetail from "@/features/subcontractors/components/SubcontractorDetail";

export async function generateStaticParams() {
    return [{ id: "placeholder" }];
}

export default function SubcontractorDetailsPage() {
    return <SubcontractorDetail />;
}
