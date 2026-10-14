import { useState } from "react";
import { useLanguage } from "@/context/language";
import { toast } from "sonner";
import { submitWaitlist } from "@/lib/publicForms";
import { toastSaveError } from "@/lib/formServerErrors";

/** The waitlist's required fields, in form order; their inputs have these ids. */
export const WAITLIST_FIELDS = ["fullName", "email", "phone"] as const;
export type WaitlistField = (typeof WAITLIST_FIELDS)[number];

/** Something@something: the shape only; Go validates the post again. */
const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+$/;

/** The message key of each required field that fails, in form order. */
export function waitlistErrors(values: Record<WaitlistField, string>): Partial<Record<WaitlistField, string>> {
  const errors: Partial<Record<WaitlistField, string>> = {};
  if (!values.fullName.trim()) errors.fullName = "auth.waitlist.nameRequired";
  const email = values.email.trim();
  if (!email) errors.email = "auth.emailRequired";
  else if (!EMAIL_SHAPE.test(email)) errors.email = "auth.emailInvalid";
  if (!values.phone.trim()) errors.phone = "auth.waitlist.phoneRequired";
  return errors;
}

/**
 * The state of the `/waitlist` page's form. It posts to the anonymous BFF form `POST /api/forms/waitlist`
 * (R44, R77; lib/publicForms.ts), which forwards to Go's internal `POST /v1/waitlist`; nothing is written
 * to Firestore. `website` is the hidden honeypot field.
 *
 * UI3E (DESIGN.md form style): a missing or malformed value is an error under its field, not a browser popup; the
 * first one takes the focus and one toast sums them up (the shared `common.toast.validationError` text). A sent form
 * shows the success card, with no toast as well.
 */
export function useWaitlist() {
  const [email, setEmailValue] = useState("");
  const [fullName, setFullNameValue] = useState("");
  const [countryCode, setCountryCode] = useState("+66");
  const [phone, setPhoneValue] = useState("");
  const [website, setWebsite] = useState("");
  const [loading, setLoading] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [errors, setErrors] = useState<Partial<Record<WaitlistField, string>>>({});
  const { t } = useLanguage();

  // A field's error goes once it is edited; the next submit checks again.
  const edited = (field: WaitlistField) => setErrors((prev) => (prev[field] ? { ...prev, [field]: undefined } : prev));
  const setEmail = (value: string) => {
    setEmailValue(value);
    edited("email");
  };
  const setFullName = (value: string) => {
    setFullNameValue(value);
    edited("fullName");
  };
  const setPhone = (value: string) => {
    setPhoneValue(value);
    edited("phone");
  };

  const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const found = waitlistErrors({ fullName, email, phone });
    setErrors(found);
    const failed = WAITLIST_FIELDS.filter((field) => found[field]);
    if (failed.length > 0) {
      const first = failed[0];
      toast.error(t("common.toast.validationError", { count: failed.length, field: t(found[first] ?? "") }));
      e.currentTarget.querySelector<HTMLInputElement>(`#${first}`)?.focus();
      return;
    }

    setLoading(true);
    try {
      await submitWaitlist({
        email: email.trim(),
        name: fullName.trim(),
        countryCode: countryCode || "+66",
        phone: phone.trim(),
        website,
      });
      setSubmitted(true);
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
    errors,
    handleSubmit,
    t
  };
}
