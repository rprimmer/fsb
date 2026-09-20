// Column layout (order and widths) for the file table. Pure functions only, so
// they can be tested without a browser; App.svelte does the localStorage I/O.

export type ColId = 'name' | 'size' | 'modTime' | 'kind' | 'mode';
export type SortKey = Exclude<ColId, 'mode'>;

export const COLUMN_IDS: readonly ColId[] = ['name', 'size', 'modTime', 'kind', 'mode'];

export const COLUMNS: Record<ColId, { label: string; cls: string }> = {
  name: { label: 'Name', cls: 'c-name' },
  size: { label: 'Size', cls: 'c-size' },
  modTime: { label: 'Modified', cls: 'c-date' },
  kind: { label: 'Kind', cls: 'c-kind' },
  mode: { label: 'Permissions', cls: 'c-mode' },
};

export const DEFAULT_WIDTHS: Readonly<Record<ColId, number>> = {
  name: 360,
  size: 90,
  modTime: 170,
  kind: 130,
  mode: 110,
};

export const MIN_WIDTH = 60;
export const MAX_WIDTH = 900;
/** Must match the grid `gap` and row padding in app.css. */
const GAP = 12;
const PADDING = 32;

export interface Layout {
  order: ColId[];
  widths: Record<ColId, number>;
}

export function isSortable(id: ColId): id is SortKey {
  return id !== 'mode';
}

export function defaultLayout(): Layout {
  return { order: [...COLUMN_IDS], widths: { ...DEFAULT_WIDTHS } };
}

export function clampWidth(w: number): number {
  if (!Number.isFinite(w)) return MIN_WIDTH;
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(w)));
}

/**
 * Reads a saved layout. Anything malformed (bad JSON, a wrong or duplicated
 * column list, out-of-range or non-numeric widths) falls back to defaults
 * per field, so a corrupt value can never break the table.
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
  const { order, widths } = raw as { order?: unknown; widths?: unknown };

  if (
    Array.isArray(order) &&
    order.length === COLUMN_IDS.length &&
    COLUMN_IDS.every((id) => order.includes(id))
  ) {
    layout.order = [...(order as ColId[])];
  }
  if (typeof widths === 'object' && widths !== null) {
    for (const id of COLUMN_IDS) {
      const v = (widths as Record<string, unknown>)[id];
      if (typeof v === 'number' && Number.isFinite(v)) layout.widths[id] = clampWidth(v);
    }
  }
  return layout;
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

/** Moves a column one step (or more) left (-) or right (+), stopping at the ends. */
export function moveBy(order: ColId[], id: ColId, delta: number): ColId[] {
  const i = order.indexOf(id);
  if (i < 0) return order;
  const j = Math.min(order.length - 1, Math.max(0, i + delta));
  return j === i ? order : moveColumn(order, id, order[j]);
}

/** CSS grid-template-columns: fixed columns plus a flexible filler that takes the slack. */
export function gridTemplate(l: Layout): string {
  return l.order.map((id) => `${l.widths[id]}px`).join(' ') + ' minmax(0, 1fr)';
}

/** Minimum table width in px, so narrow windows scroll sideways instead of clipping. */
export function totalWidth(l: Layout): number {
  const sum = l.order.reduce((n, id) => n + l.widths[id], 0);
  return sum + GAP * l.order.length + PADDING;
}
