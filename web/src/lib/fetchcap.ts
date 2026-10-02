// Reading responses within a byte budget. No DOM, so `node --test` can test it.

/**
 * The body of r as a Blob (typed from its Content-Type), or null if it is
 * larger than max bytes. A declared Content-Length over max is refused before
 * reading; the header is not trusted otherwise, so the body is counted as it
 * arrives and canceled as soon as it crosses max.
 */
export async function readCapped(r: Response, max: number): Promise<Blob | null> {
  const type = r.headers.get('content-type') ?? '';
  const declared = Number(r.headers.get('content-length'));
  if (!r.body || declared > max) {
    await r.body?.cancel().catch(() => {});
    return r.body ? null : new Blob([], { type });
  }
  const reader = r.body.getReader();
  const chunks: Uint8Array<ArrayBuffer>[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > max) {
      await reader.cancel().catch(() => {});
      return null;
    }
    chunks.push(value as Uint8Array<ArrayBuffer>);
  }
  return new Blob(chunks, { type });
}

/** fn applied to every item, at most n at a time; results in input order. */
export async function mapLimit<T, R>(items: T[], n: number, fn: (item: T) => Promise<R>): Promise<R[]> {
  const out = new Array<R>(items.length);
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const i = next++;
      out[i] = await fn(items[i]);
    }
  };
  await Promise.all(Array.from({ length: Math.min(n, items.length) }, worker));
  return out;
}
