// The anonymous waitlist form (R44, R77; developer-spec.md §10.4, Appendix E §E.8.3): Origin-checked, no
// cookie, no Authorization, forwarded to Go's internal `POST /v1/waitlist`. Logic: lib/bff/forms.ts.
import { formOptions, postForm } from "@/lib/bff/forms";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export const POST = (request: Request) => postForm(request, "waitlist");
export const OPTIONS = formOptions;
