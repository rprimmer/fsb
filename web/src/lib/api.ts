import { apiPath } from './base.ts';

export { apiPath };

export interface Entry {
  name: string;
  isDir: boolean;
  isSymlink?: boolean;
  broken?: boolean;
  size: number;
  modTime: string;
  /** Go fs.FileMode; the low 9 bits are the permission bits. */
  mode: number;
}

/** A listing row, or a search hit (which also carries its full path and its path relative to the search root). */
export interface Row extends Entry {
  path?: string;
  rel?: string;
}

export interface Status {
  readOnly: boolean;
  coreDenyMissing: string[];
  roots: string[];
  /** The user's real home directory, for expanding ~. */
  home: string;
}

export interface XAttr {
  name: string;
  size?: number;
  value?: string;
  encoding?: 'utf8' | 'hex';
  large?: boolean;
}

export interface Meta extends Entry {
  path: string;
  symlinkTarget?: string;
  dataless?: boolean;
  xattrs: XAttr[];
}

export interface Head {
  kind: 'text' | 'binary' | 'empty' | 'dataless';
  size: number;
  text?: string;
  truncated?: boolean;
}

export interface ArchiveEntry {
  name: string;
  size: number;
  isDir: boolean;
  modTime?: string;
}

export interface ArchiveListing {
  format: string;
  entries: ArchiveEntry[];
  total: number;
  truncated?: boolean;
  incomplete?: boolean;
}

export interface SearchDone {
  visited: number;
  truncated: boolean;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function failure(resp: Response): Promise<ApiError> {
  const text = (await resp.text().catch(() => '')).trim();
  return new ApiError(resp.status, text || resp.statusText);
}

async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const resp = await fetch(url, { signal });
  if (!resp.ok) throw await failure(resp);
  return resp.json();
}

const q = (path: string) => encodeURIComponent(path);

export function getStatus(): Promise<Status> {
  return getJSON(apiPath('api/status'));
}

/** A bounded, classified look at the start of a file. Binary and cloud-only files never return content. */
export function getHead(path: string, bytes: number, signal?: AbortSignal): Promise<Head> {
  return getJSON(apiPath(`api/head?path=${q(path)}&bytes=${bytes}`), signal);
}

/** The table of contents of a zip, tar or tar.gz file. Nothing is extracted. */
export function getArchive(path: string, signal?: AbortSignal): Promise<ArchiveListing> {
  return getJSON(apiPath(`api/archive?path=${q(path)}`), signal);
}

/** Details and extended attributes. With values=false only attribute names are returned. */
export function getMeta(path: string, values: boolean, signal?: AbortSignal): Promise<Meta> {
  return getJSON(apiPath(`api/meta?path=${q(path)}${values ? '' : '&values=0'}`), signal);
}

/** URL of an inline (sandboxed) image preview. The server refuses anything but PNG, JPEG, GIF and WebP. */
export function previewURL(path: string): string {
  return apiPath(`api/preview?path=${q(path)}`);
}

export function quicklookURL(path: string): string {
  return apiPath(`api/quicklook?path=${q(path)}`);
}

export function pdfURL(path: string): string {
  return apiPath(`api/pdf?path=${q(path)}`);
}

export function fileURL(path: string): string {
  return apiPath(`api/file?path=${q(path)}`);
}

/** Reads an NDJSON response line by line, calling handle for each parsed object. */
async function streamNDJSON(resp: Response, handle: (msg: Record<string, unknown>) => void): Promise<void> {
  if (!resp.body) throw new Error('streaming is not supported by this browser');
  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buffered = '';
  const line = (s: string) => {
    if (!s) return;
    const msg = JSON.parse(s) as Record<string, unknown>;
    if (typeof msg.error === 'string') throw new Error(msg.error);
    handle(msg);
  };
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffered += decoder.decode(value, { stream: true });
    let nl: number;
    while ((nl = buffered.indexOf('\n')) >= 0) {
      line(buffered.slice(0, nl));
      buffered = buffered.slice(nl + 1);
    }
  }
  buffered += decoder.decode();
  line(buffered.trim());
}

/**
 * Streams a directory listing (NDJSON). onEntries is called once per chunk in
 * directory order; the caller sorts.
 */
export async function streamList(
  path: string,
  onEntries: (entries: Entry[]) => void,
  signal: AbortSignal,
): Promise<void> {
  const resp = await fetch(apiPath(`api/list?path=${q(path)}`), { signal });
  if (!resp.ok) throw await failure(resp);
  await streamNDJSON(resp, (m) => {
    if (Array.isArray(m.entries)) onEntries(m.entries as Entry[]);
  });
}

/**
 * Streams filename matches under root, shallowest first. Resolves with what
 * the server visited and whether a limit cut the search short.
 */
export async function streamSearch(
  root: string,
  query: string,
  matchCase: boolean,
  onMatches: (rows: Row[]) => void,
  signal: AbortSignal,
): Promise<SearchDone> {
  const resp = await fetch(apiPath(`api/search?path=${q(root)}&q=${encodeURIComponent(query)}${matchCase ? '&case=1' : ''}`), { signal });
  if (!resp.ok) throw await failure(resp);
  let done: SearchDone = { visited: 0, truncated: false };
  await streamNDJSON(resp, (m) => {
    if (Array.isArray(m.matches)) onMatches(m.matches as Row[]);
    if (m.done === true) done = { visited: Number(m.visited) || 0, truncated: m.truncated === true };
  });
  return done;
}
