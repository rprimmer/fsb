// Name matching for the filter box (and shared with the folding used elsewhere). Pure, so it can be tested without a browser.

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
  return matchCase ? n.includes(q) : fold(n).includes(fold(q));
}

/**
 * Case folding as APFS (and so the server's search) does it: full folding, in
 * which "ß" and "ss", "ſ" and "s", the Kelvin sign and "k", and ligatures such
 * as "ﬁ" and "fi" are equal. Lower-casing, upper-casing (which expands "ß" and the
 * ligatures) and lower-casing again folds the result, which agrees with full case folding on
 * every alias measured on APFS (see the tests).
 */
export function fold(s: string): string {
  return s.normalize('NFC').toLowerCase().toUpperCase().toLowerCase().normalize('NFC');
}
