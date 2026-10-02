// The lifecycle of one streamed search, kept out of App.svelte so it can be
// tested: results are batched for display, and once the search is canceled
// (replaced, cleared, or navigated away from) nothing of it is ever delivered
// again, including a batch waiting on the flush timer.

import type { Row } from './api.ts';

export interface SearchSink {
  /** All rows so far (a new array each time). */
  rows(all: Row[]): void;
  done(truncated: boolean): void;
  error(e: unknown): void;
}

export type SearchStream = (onRows: (rows: Row[]) => void, signal: AbortSignal) => Promise<{ truncated: boolean }>;

/**
 * Runs stream, delivering to sink: the first batch at once, later ones at most
 * every flushMs. Returns a function that cancels the search for good.
 */
export function startSearch(stream: SearchStream, sink: SearchSink, flushMs = 120): () => void {
  const ctrl = new AbortController();
  let live = true;
  let buf: Row[] = [];
  let acc: Row[] = [];
  let timer: ReturnType<typeof setTimeout> | undefined;
  const flush = () => {
    timer = undefined;
    if (!live || !buf.length) return;
    acc = acc.concat(buf);
    buf = [];
    sink.rows(acc);
  };
  const end = () => {
    clearTimeout(timer);
    flush();
    live = false;
  };
  stream((rows) => {
    if (!live) return;
    buf.push(...rows);
    if (acc.length === 0) flush();
    else timer ??= setTimeout(flush, flushMs);
  }, ctrl.signal).then(
    (d) => {
      if (!live) return;
      end();
      sink.done(d.truncated);
    },
    (e) => {
      if (!live) return;
      end();
      sink.error(e);
    },
  );
  return () => {
    live = false;
    clearTimeout(timer);
    ctrl.abort();
  };
}
