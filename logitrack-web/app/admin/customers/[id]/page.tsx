import CustomerDetail from "@/features/customers/components/CustomerDetail";

export async function generateStaticParams() {
    return [{ id: "placeholder" }];
}

export default function CustomerDetailPage() {
    return <CustomerDetail />;
}
