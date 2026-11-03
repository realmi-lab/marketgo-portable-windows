export function clampPage(
  page: number,
  totalRows: number,
  pageSize: number
): number {
  const pages = Math.max(
    1,
    Math.ceil(Math.max(0, totalRows) / Math.max(1, pageSize))
  );
  return Math.min(Math.max(1, Math.trunc(page) || 1), pages);
}
