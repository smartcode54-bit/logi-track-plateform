export function renewalAmountError(value: string, completed: boolean): string | null {
    if (!value.trim()) return completed ? "renewals.form.amountRequired" : null;
    const amount = Number(value);
    if (!Number.isFinite(amount) || amount < 0 || amount > 1000000) return "renewals.form.amountInvalid";
    if (Math.abs(amount * 100 - Math.round(amount * 100)) > 0.000001) return "renewals.form.amountPrecision";
    return null;
}
