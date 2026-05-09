import SubcontractorEdit from "@/features/subcontractors/components/SubcontractorEdit";

export async function generateStaticParams() {
    return [{ id: "placeholder" }];
}

export default function EditSubcontractorPage() {
    return <SubcontractorEdit />;
}
