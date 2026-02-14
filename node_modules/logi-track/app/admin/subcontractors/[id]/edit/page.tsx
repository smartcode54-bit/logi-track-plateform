import SubcontractorEditClient from "./SubcontractorEditClient";

export async function generateStaticParams() {
    return [{ id: "placeholder" }];
}

export default function EditSubcontractorPage() {
    return <SubcontractorEditClient />;
}
