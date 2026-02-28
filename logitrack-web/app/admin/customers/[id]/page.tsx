import CustomerDetailClient from "./CustomerDetailClient";

export async function generateStaticParams() {
    return [{ id: "placeholder" }];
}

export default function CustomerDetailPage() {
    return <CustomerDetailClient />;
}
