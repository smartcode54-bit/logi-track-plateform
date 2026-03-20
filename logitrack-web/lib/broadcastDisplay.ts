/**
 * Stored subject only. Legacy docs without `title` show "—" so the message column
 * is not duplicated in the title column.
 */
export function displayBroadcastTitle(title: string | undefined): string {
  const t = title?.trim();
  return t || "—";
}
