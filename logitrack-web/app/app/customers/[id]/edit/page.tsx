import EditCustomerForm from "@/features/customers/components/EditCustomerForm";

export async function generateStaticParams() {
    return [{ id: "placeholder" }];
}

export default function EditCustomerPage() {
    return <EditCustomerForm />;
}
