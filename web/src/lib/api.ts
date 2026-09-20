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

export interface Status {
  readOnly: boolean;
  coreDenyMissing: string[];
  roots: string[];
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

export async function getStatus(): Promise<Status> {
  const resp = await fetch('/api/status');
  if (!resp.ok) throw await failure(resp);
  return resp.json();
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
  const resp = await fetch(`/api/list?path=${encodeURIComponent(path)}`, { signal });
  if (!resp.ok) throw await failure(resp);
  if (!resp.body) throw new Error('streaming is not supported by this browser');

  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buffered = '';
  const handle = (line: string) => {
    if (!line) return;
    const msg = JSON.parse(line) as { entries?: Entry[]; error?: string };
    if (msg.error) throw new Error(msg.error);
    if (msg.entries) onEntries(msg.entries);
  };
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffered += decoder.decode(value, { stream: true });
    let nl: number;
    while ((nl = buffered.indexOf('\n')) >= 0) {
      handle(buffered.slice(0, nl));
      buffered = buffered.slice(nl + 1);
    }
  }
  buffered += decoder.decode();
  handle(buffered.trim());
}

export function fileURL(path: string): string {
  return `/api/file?path=${encodeURIComponent(path)}`;
}
