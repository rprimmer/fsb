// Name matching for the filter box. Pure, so it can be tested without a browser.

/**
 * Whether name contains query. An empty query matches everything. Both sides
 * are compared after Unicode normalization (macOS may store an accented name in
 * a different form than the user types it in), and case is ignored unless
 * matchCase is set. This works on any volume: it is text comparison, not a
 * property of the filesystem.
 */
export function matchesName(name: string, query: string, matchCase: boolean): boolean {
  const q = query.trim().normalize('NFC');
  if (!q) return true;
  const n = name.normalize('NFC');
  return matchCase ? n.includes(q) : n.toLowerCase().includes(q.toLowerCase());
}
