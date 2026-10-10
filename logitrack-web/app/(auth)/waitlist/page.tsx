"use client";

// Straight from the component: the features/auth barrel would also bring the sign-in panel (GIS,
// change password) of LoginForm into this page's initial JS.
import WaitlistForm from "@/features/auth/components/WaitlistForm";

export default function WaitlistPage() {
  return <WaitlistForm />;
}

