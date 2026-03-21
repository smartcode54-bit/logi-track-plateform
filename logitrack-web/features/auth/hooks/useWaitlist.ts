import { useState } from "react";
import { useLanguage } from "@/context/language";
import { toast } from "sonner";
import { collection, addDoc, serverTimestamp } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";

export function useWaitlist() {
  const [email, setEmail] = useState("");
  const [fullName, setFullName] = useState("");
  const [countryCode, setCountryCode] = useState("+66");
  const [phone, setPhone] = useState("");
  const [loading, setLoading] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const { t } = useLanguage();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);

    try {
      if (!email) {
        toast.error("Please enter your email address");
        setLoading(false);
        return;
      }

      if (!fullName.trim()) {
        toast.error("Please enter your name");
        setLoading(false);
        return;
      }

      if (!phone.trim()) {
        toast.error("Please enter your phone number");
        setLoading(false);
        return;
      }

      await addDoc(collection(db, COLLECTIONS.WAITLIST), {
        email,
        name: fullName.trim(),
        countryCode: countryCode || "+66",
        phone: phone.trim(),
        createdAt: serverTimestamp(),
      });

      setSubmitted(true);
      toast.success("Successfully joined the waitlist!");
    } catch (error: any) {
      console.error("Error joining waitlist:", error);
      toast.error(error.message || "Failed to join waitlist. Please try again.");
    } finally {
      setLoading(false);
    }
  };

  return {
    email,
    setEmail,
    fullName,
    setFullName,
    countryCode,
    setCountryCode,
    phone,
    setPhone,
    loading,
    submitted,
    handleSubmit,
    t
  };
}
