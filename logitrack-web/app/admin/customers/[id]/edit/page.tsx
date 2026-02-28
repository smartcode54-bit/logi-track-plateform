import EditCustomerClient from "./EditCustomerClient";

export async function generateStaticParams() {
    return [{ id: "placeholder" }];
}

export default function EditCustomerPage() {
    return <EditCustomerClient />;
}
