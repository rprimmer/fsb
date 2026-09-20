// Column layout (order, widths, visibility) for the file table. Pure functions
// only, so they can be tested without a browser; App.svelte does the
// localStorage I/O.

export type ColId = 'name' | 'size' | 'modTime' | 'kind' | 'mode' | 'xattr';
export type SortKey = Exclude<ColId, 'mode' | 'xattr'>;

export const COLUMN_IDS: readonly ColId[] = ['name', 'size', 'modTime', 'kind', 'mode', 'xattr'];

export const COLUMNS: Record<ColId, { label: string; cls: string }> = {
  name: { label: 'Name', cls: 'c-name' },
  size: { label: 'Size', cls: 'c-size' },
  modTime: { label: 'Modified', cls: 'c-date' },
  kind: { label: 'Kind', cls: 'c-kind' },
  mode: { label: 'Permissions', cls: 'c-mode' },
  xattr: { label: 'Attributes', cls: 'c-xattr' },
};

export const DEFAULT_WIDTHS: Readonly<Record<ColId, number>> = {
  name: 360,
  size: 90,
  modTime: 170,
  kind: 130,
  mode: 110,
  xattr: 240,
};

/** Hidden until the user turns them on. */
export const DEFAULT_HIDDEN: readonly ColId[] = ['xattr'];

export const MIN_WIDTH = 60;
export const MAX_WIDTH = 900;
/** Must match the grid `gap` and row padding in app.css. */
const GAP = 12;
const PADDING = 32;

export interface Layout {
  /** Every column, including hidden ones, so a hidden column keeps its place. */
  order: ColId[];
  widths: Record<ColId, number>;
  hidden: ColId[];
}

export function isSortable(id: ColId): id is SortKey {
  return id !== 'mode' && id !== 'xattr';
}

export function defaultLayout(): Layout {
  return { order: [...COLUMN_IDS], widths: { ...DEFAULT_WIDTHS }, hidden: [...DEFAULT_HIDDEN] };
}

export function clampWidth(w: number): number {
  if (!Number.isFinite(w)) return MIN_WIDTH;
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(w)));
}

const isColId = (v: unknown): v is ColId => typeof v === 'string' && (COLUMN_IDS as readonly string[]).includes(v);

/**
 * Reads a saved layout. It is forgiving so that old saves survive new columns
 * and a corrupt value can never break the table: unknown or duplicate column
 * ids are dropped, missing ones are appended in default order, widths are
 * clamped, and every field falls back to its default independently.
 */
export function parseLayout(json: string | null): Layout {
  const layout = defaultLayout();
  if (!json) return layout;
  let raw: unknown;
  try {
    raw = JSON.parse(json);
  } catch {
    return layout;
  }
  if (typeof raw !== 'object' || raw === null) return layout;
  const { order, widths, hidden } = raw as { order?: unknown; widths?: unknown; hidden?: unknown };

  if (Array.isArray(order)) {
    const seen: ColId[] = [];
    for (const id of order) if (isColId(id) && !seen.includes(id)) seen.push(id);
    layout.order = [...seen, ...COLUMN_IDS.filter((id) => !seen.includes(id))];
  }
  if (typeof widths === 'object' && widths !== null) {
    for (const id of COLUMN_IDS) {
      const v = (widths as Record<string, unknown>)[id];
      if (typeof v === 'number' && Number.isFinite(v)) layout.widths[id] = clampWidth(v);
    }
  }
  if (Array.isArray(hidden)) {
    layout.hidden = COLUMN_IDS.filter((id) => id !== 'name' && hidden.includes(id));
  }
  return layout;
}

/** Columns currently shown, in order. Name can never be hidden. */
export function visibleOrder(l: Layout): ColId[] {
  return l.order.filter((id) => id === 'name' || !l.hidden.includes(id));
}

export function isHidden(l: Layout, id: ColId): boolean {
  return id !== 'name' && l.hidden.includes(id);
}

export function toggleColumn(l: Layout, id: ColId): Layout {
  if (id === 'name') return l;
  const hidden = l.hidden.includes(id) ? l.hidden.filter((h) => h !== id) : [...l.hidden, id];
  return { ...l, hidden };
}

/** Moves `from` to the position `to` currently occupies (array-move semantics). */
export function moveColumn(order: ColId[], from: ColId, to: ColId): ColId[] {
  const i = order.indexOf(from);
  const j = order.indexOf(to);
  if (i < 0 || j < 0 || i === j) return order;
  const next = [...order];
  next.splice(i, 1);
  next.splice(j, 0, from);
  return next;
}

/**
 * Moves a column past its visible neighbours (hidden columns are skipped, so a
 * keypress always changes what the user sees), stopping at the ends.
 */
export function moveBy(l: Layout, id: ColId, delta: number): ColId[] {
  const vis = visibleOrder(l);
  const i = vis.indexOf(id);
  if (i < 0) return l.order;
  const j = Math.min(vis.length - 1, Math.max(0, i + delta));
  return j === i ? l.order : moveColumn(l.order, id, vis[j]);
}

/** CSS grid-template-columns: fixed columns plus a flexible filler that takes the slack. */
export function gridTemplate(l: Layout): string {
  return visibleOrder(l).map((id) => `${l.widths[id]}px`).join(' ') + ' minmax(0, 1fr)';
}

/** Minimum table width in px, so narrow windows scroll sideways instead of clipping. */
export function totalWidth(l: Layout): number {
  const vis = visibleOrder(l);
  const sum = vis.reduce((n, id) => n + l.widths[id], 0);
  return sum + GAP * vis.length + PADDING;
}
