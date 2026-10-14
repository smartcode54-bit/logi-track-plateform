/**
 * The Signal Red star of a required field's label (DESIGN.md, form style). Hidden from assistive technology: the
 * field itself says it is required (`aria-required`), so the star is not read out as "star".
 */
export function RequiredMark() {
    return (
        <span aria-hidden className="text-destructive">
            {" *"}
        </span>
    );
}
