"use client";

import { HONEYPOT_FIELD } from "@/lib/publicForms";

/**
 * The hidden honeypot of the anonymous forms (T54R; Go `forms.HoneypotField`): off-screen, out of the
 * tab order and of the accessibility tree, so a person never fills it while a form-filling bot usually
 * does. Go answers a post that sets it like any other and stores nothing.
 */
export function HoneypotField({ id, value, onChange }: { id: string; value: string; onChange: (value: string) => void }) {
    return (
        <div aria-hidden="true" className="absolute -left-[10000px] top-auto size-px overflow-hidden">
            <label htmlFor={id}>Website</label>
            <input id={id} name={HONEYPOT_FIELD} type="text" tabIndex={-1} autoComplete="off" value={value} onChange={(e) => onChange(e.target.value)} />
        </div>
    );
}
