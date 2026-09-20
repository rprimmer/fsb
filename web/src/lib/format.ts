import type { Entry } from './api';

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
  const root = roots
    .filter((r) => path === r || path.startsWith(r === '/' ? '/' : r + '/'))
    .sort((a, b) => b.length - a.length)[0];
  if (!root) return [{ label: path, path }];
  const crumbs: Crumb[] = [{ label: root, path: root }];
  let cur = root;
  for (const part of path.slice(root.length).split('/').filter(Boolean)) {
    cur = joinPath(cur, part);
    crumbs.push({ label: part, path: cur });
  }
  return crumbs;
}
