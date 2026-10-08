import type { Entry } from './api';
import { fold } from './filter.ts';

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'];

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  let v = bytes;
  let u = 0;
  while (v >= 1024 && u < UNITS.length - 1) {
    v /= 1024;
    u++;
  }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} ${UNITS[u]}`;
}

const dateFmt = new Intl.DateTimeFormat(undefined, {
  year: 'numeric',
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
});

export function formatDate(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : dateFmt.format(d);
}

/** e.g. "drwxr-xr-x" from the entry's flags and permission bits. */
export function modeString(e: Entry): string {
  const type = e.isSymlink ? 'l' : e.isDir ? 'd' : '-';
  const bits = 'rwxrwxrwx';
  let out = type;
  for (let i = 0; i < 9; i++) out += e.mode & (1 << (8 - i)) ? bits[i] : '-';
  return out;
}

export function kindOf(e: Entry): string {
  if (e.broken) return 'Broken link';
  if (e.isDir) return e.isSymlink ? 'Folder link' : 'Folder';
  const dot = e.name.lastIndexOf('.');
  const ext = dot > 0 && dot < e.name.length - 1 ? e.name.slice(dot + 1).toLowerCase() : '';
  const base = ext ? `${ext.toUpperCase()} file` : 'File';
  return e.isSymlink ? `${base} link` : base;
}

export function joinPath(dir: string, name: string): string {
  return dir === '/' ? `/${name}` : `${dir}/${name}`;
}

/**
 * Location hash for a path: "#/Users/me/My%20Docs", optionally followed by
 * "?select=name" to open the folder with an entry selected. Never sent to the
 * server. A literal "?" inside a path is always encoded (%3F), so the first
 * "?" in the hash unambiguously starts the options.
 */
export function pathToHash(path: string, select?: string): string {
  const base = '#' + path.split('/').map(encodeURIComponent).join('/');
  return select ? `${base}?select=${encodeURIComponent(select)}` : base;
}

export function parseHash(hash: string): { path: string; select: string } {
  const none = { path: '', select: '' };
  if (!hash.startsWith('#/')) return none;
  const q = hash.indexOf('?');
  const pathPart = q < 0 ? hash : hash.slice(0, q);
  let path: string;
  try {
    path = pathPart.slice(1).split('/').map(decodeURIComponent).join('/');
  } catch {
    return none;
  }
  // Parsed strictly (URLSearchParams would turn a bad escape into U+FFFD). A
  // malformed selection is ignored; the folder itself is still valid.
  let select = '';
  if (q >= 0) {
    for (const part of hash.slice(q + 1).split('&')) {
      const eq = part.indexOf('=');
      if (eq > 0 && part.slice(0, eq) === 'select') {
        try {
          select = decodeURIComponent(part.slice(eq + 1));
        } catch {
          select = '';
        }
      }
    }
  }
  return { path, select };
}

export function hashToPath(hash: string): string {
  return parseHash(hash).path;
}

export function dirname(path: string): string {
  const i = path.lastIndexOf('/');
  return i <= 0 ? '/' : path.slice(0, i);
}

export function basename(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1);
}

export interface Crumb {
  label: string;
  path: string;
}

/**
 * Breadcrumbs for path. The first crumb is the enclosing root (shown as its
 * full path), since nothing above a root is reachable.
 */
export function crumbsFor(path: string, roots: string[]): Crumb[] {
  // Compared as the file system compares names, so a path typed in another case
  // still finds its root.
  const fp = fold(path);
  const root = roots
    .filter((r) => fp === fold(r) || fp.startsWith(r === '/' ? '/' : fold(r) + '/'))
    .sort((a, b) => b.length - a.length)[0];
  if (!root) return [{ label: path, path }];
  const crumbs: Crumb[] = [{ label: root, path: root }];
  let cur = root;
  // By components, not characters: a folded spelling can differ in length from the root's.
  const skip = root.split('/').filter(Boolean).length;
  for (const part of path.split('/').filter(Boolean).slice(skip)) {
    cur = joinPath(cur, part);
    crumbs.push({ label: part, path: cur });
  }
  return crumbs;
}

// Characters that change what a name looks like without being visible: controls,
// bidirectional overrides and isolates (which reverse the text that follows, so
// "report\u202Etxt.exe" reads as "reportexe.txt"), line and paragraph separators,
// the byte order mark, and the invisible "tag" characters.
const DECEPTIVE = /[\u0000-\u001f\u007f-\u009f\u2028\u2029\u202a-\u202e\u2066-\u2069\ufeff\u{e0000}-\u{e007f}]/gu;

/**
 * A name as it should be shown: characters that would disguise it are replaced by
 * a visible marker such as "‹U+202E›". Display only; the real name is what links
 * and downloads use. Legitimate right-to-left text is untouched.
 */
export function displayName(name: string): string {
  return name
    .replace(RAW_BYTE, (_, hex) => `\u20390x${hex}\u203a`)
    .replace(DECEPTIVE, (c) => `\u2039U+${c.codePointAt(0)!.toString(16).toUpperCase().padStart(4, '0')}\u203a`);
}

// A byte of a name that is not UTF-8, as the server sends it: NUL and two
// uppercase hex digits (server/wire.go). NUL never occurs in a real name.
const RAW_BYTE = /\u0000([0-9A-F]{2})/g;

/**
 * A path as a URL query value: escaped bytes become themselves (%E9), so the
 * server receives the name's real bytes; everything else is sent as UTF-8.
 */
export function queryPath(path: string): string {
  return path
    .split(RAW_BYTE)
    .map((part, i) => (i % 2 === 1 ? `%${part}` : encodeURIComponent(part)))
    .join('');
}

/**
 * A path to put on the clipboard. One with bytes that are not UTF-8 cannot be
 * written as plain text, so it is quoted for the shell (bash, zsh): $'...\xE9...'.
 */
export function copyablePath(path: string): string {
  if (!path.includes('\u0000')) return path;
  const quoted = path.replace(/[\\']/g, (c) => `\\${c}`).replace(RAW_BYTE, (_, hex) => `\\x${hex}`);
  return `$'${quoted}'`;
}
