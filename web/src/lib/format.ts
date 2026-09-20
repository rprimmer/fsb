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

/** Location hash for a path: "#/Users/me/My%20Docs". Never sent to the server. */
export function pathToHash(path: string): string {
  return '#' + path.split('/').map(encodeURIComponent).join('/');
}

export function hashToPath(hash: string): string {
  if (!hash.startsWith('#/')) return '';
  try {
    return hash.slice(1).split('/').map(decodeURIComponent).join('/');
  } catch {
    return '';
  }
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
