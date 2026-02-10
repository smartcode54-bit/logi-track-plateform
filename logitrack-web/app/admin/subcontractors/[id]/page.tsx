import SubcontractorDetailClient from "./SubcontractorDetailClient";

export async function generateStaticParams() {
    return [{ id: "placeholder" }];
}

export default function SubcontractorDetailsPage() {
    return <SubcontractorDetailClient />;
}
