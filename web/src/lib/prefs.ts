// Per-viewer preferences kept in localStorage. Pure functions so they can be
// tested without a browser; App.svelte does the storage I/O.

export const DEFAULT_PANE_WIDTH = 380;
export const MIN_PANE_WIDTH = 240;
export const MAX_PANE_WIDTH = 900;

export interface Prefs {
  preview: boolean;
  hover: boolean;
  previewWidth: number;
}

export function clampPaneWidth(w: number): number {
  if (!Number.isFinite(w)) return DEFAULT_PANE_WIDTH;
  return Math.min(MAX_PANE_WIDTH, Math.max(MIN_PANE_WIDTH, Math.round(w)));
}

/**
 * Reads saved preferences. Each field falls back to its default on its own, so
 * a corrupt or old save can never break the page. The preview pane starts open
 * only on a window wide enough to hold it beside the list.
 */
export function parsePrefs(json: string | null, windowWidth: number): Prefs {
  const prefs: Prefs = { preview: windowWidth >= 900, hover: true, previewWidth: DEFAULT_PANE_WIDTH };
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
  if (typeof r.previewWidth === 'number' && Number.isFinite(r.previewWidth)) prefs.previewWidth = clampPaneWidth(r.previewWidth);
  return prefs;
}
