// Per-viewer preferences kept in localStorage. Pure functions so they can be
// tested without a browser; App.svelte does the storage I/O.

export const DEFAULT_PANE_WIDTH = 380;
export const MIN_PANE_WIDTH = 240;
/** Space always left for the listing side when the pane's width is bounded by
 * the window, so the list never gets squeezed away. Matches the CSS safety
 * net (`max-width: calc(100vw - 320px)` on `.preview`); keep the two in sync. */
export const RESERVED_FOR_LIST = 320;
/**
 * A defensive absolute ceiling, used only when no window width is known (a
 * caller that has not measured one yet). In the browser the real ceiling
 * is the window's own width minus RESERVED_FOR_LIST (see clampPaneWidth), so
 * the pane can grow with the window rather than stopping at a fixed number.
 */
export const MAX_PANE_WIDTH = 4000;

export function clampPaneWidth(w: number, windowWidth?: number): number {
  if (!Number.isFinite(w)) return DEFAULT_PANE_WIDTH;
  const max = windowWidth !== undefined && Number.isFinite(windowWidth) ? Math.max(MIN_PANE_WIDTH, windowWidth - RESERVED_FOR_LIST) : MAX_PANE_WIDTH;
  return Math.min(max, Math.max(MIN_PANE_WIDTH, Math.round(w)));
}

export interface Prefs {
  preview: boolean;
  hover: boolean;
  previewWidth: number;
  /** List folders before files, whatever the sort column. Off by default (matches ls and eza). */
  foldersFirst: boolean;
  /** Case-sensitive filter and search. Off by default. */
  matchCase: boolean;
}

/**
 * Reads saved preferences. Each field falls back to its default on its own, so
 * a corrupt or old save can never break the page. The preview pane starts open
 * only on a window wide enough to hold it beside the list.
 */
export function parsePrefs(json: string | null, windowWidth: number): Prefs {
  const prefs: Prefs = { preview: windowWidth >= 900, hover: true, previewWidth: DEFAULT_PANE_WIDTH, foldersFirst: false, matchCase: false };
  if (!json) return prefs;
  let raw: unknown;
  try {
    raw = JSON.parse(json);
  } catch {
    return prefs;
  }
  if (typeof raw !== 'object' || raw === null) return prefs;
  const r = raw as Record<string, unknown>;
  if (typeof r.preview === 'boolean') prefs.preview = r.preview;
  if (typeof r.hover === 'boolean') prefs.hover = r.hover;
  if (typeof r.foldersFirst === 'boolean') prefs.foldersFirst = r.foldersFirst;
  if (typeof r.matchCase === 'boolean') prefs.matchCase = r.matchCase;
  if (typeof r.previewWidth === 'number' && Number.isFinite(r.previewWidth)) prefs.previewWidth = clampPaneWidth(r.previewWidth, windowWidth);
  return prefs;
}
