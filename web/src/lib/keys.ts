// Logical keys for keyboard shortcuts.

/** Keys identified by where they are on the keyboard, not by the character they produce. */
const BY_CODE = new Set(['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End', 'PageUp', 'PageDown']);

/**
 * The logical key of a keyboard event.
 *
 * `key` is unreliable for navigation keys while a modifier is held: Safari
 * driven by WebDriver reports Alt+ArrowLeft and Shift+ArrowLeft as
 * key "" (a control character), and Option can change `key` on macOS.
 * `code` names the physical key and stays correct, so navigation keys and
 * Space use it. Everything else (letters, "/", Enter, Escape, Backspace) keeps
 * `key`, which respects the user's keyboard layout.
 */
export function keyOf(ev: { key: string; code: string }): string {
  if (ev.code === 'Space') return ' ';
  if (BY_CODE.has(ev.code)) return ev.code;
  return ev.key;
}
