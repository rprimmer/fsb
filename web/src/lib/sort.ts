// Ordering of listing rows. Pure, so it can be tested without a browser.
//
// The default matches eza (and `ls` for dotfiles): names compare
// case-insensitively with numbers in natural order, and a leading dot sorts
// before letters, so `.bashrc` comes first. Folders are NOT grouped unless the
// user asks; when they do, grouping applies to every sort column the same way
// and does not flip with the sort direction.
import type { Row } from './api';
import type { SortKey } from './columns';
import { kindOf } from './format.ts';

const names = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });
const exact = new Intl.Collator(undefined, { numeric: true, sensitivity: 'variant' });

export interface SortOptions {
  key: SortKey;
  asc: boolean;
  foldersFirst: boolean;
}

const nameOf = (e: Row) => e.rel ?? e.name;

/** A folder has no size to show, so it sorts as smaller than any file. */
const sizeOf = (e: Row) => (e.isDir ? -1 : e.size);

export function compareRows(a: Row, b: Row, o: SortOptions): number {
  if (o.foldersFirst && a.isDir !== b.isDir) return a.isDir ? -1 : 1;
  let c = 0;
  switch (o.key) {
    case 'name':
      c = names.compare(nameOf(a), nameOf(b));
      break;
    case 'size':
      c = sizeOf(a) - sizeOf(b);
      break;
    case 'modTime':
      c = Date.parse(a.modTime) - Date.parse(b.modTime);
      break;
    case 'kind':
      c = names.compare(kindOf(a), kindOf(b));
      break;
  }
  if (Number.isNaN(c)) c = 0;
  // Ties fall back to the name (then to the exact name), so the order is stable and total.
  if (c === 0) c = names.compare(nameOf(a), nameOf(b)) || exact.compare(nameOf(a), nameOf(b));
  return o.asc ? c : -c;
}

/** A sorted copy of rows. */
export function sortRows(rows: Row[], o: SortOptions): Row[] {
  return [...rows].sort((a, b) => compareRows(a, b, o));
}
