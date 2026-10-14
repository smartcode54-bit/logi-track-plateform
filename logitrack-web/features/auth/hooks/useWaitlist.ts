import { useState } from "react";
import { useLanguage } from "@/context/language";
import { toast } from "sonner";
import { submitWaitlist } from "@/lib/publicForms";
import { toastSaveError } from "@/lib/formServerErrors";

/**
 * The state of the `/waitlist` page's form. It posts to the anonymous BFF form `POST /api/forms/waitlist`
 * (R44, R77; lib/publicForms.ts), which forwards to Go's internal `POST /v1/waitlist`; nothing is written
 * to Firestore. `website` is the hidden honeypot field.
 */
export function useWaitlist() {
  const [email, setEmail] = useState("");
  const [fullName, setFullName] = useState("");
  const [countryCode, setCountryCode] = useState("+66");
  const [phone, setPhone] = useState("");
  const [website, setWebsite] = useState("");
  const [loading, setLoading] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const { t } = useLanguage();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);

    try {
      if (!email) {
        toast.error(t("auth.emailRequired"));
        setLoading(false);
        return;
      }

      if (!fullName.trim()) {
        toast.error(t("auth.waitlist.nameRequired"));
        setLoading(false);
        return;
      }

      if (!phone.trim()) {
        toast.error(t("auth.waitlist.phoneRequired"));
        setLoading(false);
        return;
      }

      await submitWaitlist({
        email: email.trim(),
        name: fullName.trim(),
        countryCode: countryCode || "+66",
        phone: phone.trim(),
        website,
      });

      setSubmitted(true);
      toast.success(t("auth.waitlist.successToast"));
    } catch (error) {
      toastSaveError(error, t, "auth.waitlist.errorToast");
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
    website,
    setWebsite,
    loading,
    submitted,
    handleSubmit,
    t
  };
}
