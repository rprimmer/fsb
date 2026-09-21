// Parsing of the "Go to path" box. Pure, so it can be tested without a browser.

export type GotoResult = { path: string } | { error: string };

const ABSOLUTE_HINT = 'Enter an absolute path such as /Users/you/Documents, or start with ~/ for your home folder.';

/**
 * Turns what the user typed into an absolute, clean path. `~` and `~/x` expand
 * to home. `.`/`..` and doubled or trailing slashes are resolved lexically,
 * exactly as the server does; whether the folder exists, and whether fsb serves
 * it, is the server's decision.
 */
export function resolveGoto(input: string, home: string): GotoResult {
  let s = input.trim();
  if (!s) return { error: 'Type a folder path.' };
  if (s.includes('\0')) return { error: 'That is not a valid path.' };
  if (s === '~' || s.startsWith('~/')) {
    if (!home) return { error: 'The home folder is not known here; type the full path instead.' };
    s = home + s.slice(1);
  } else if (s.startsWith('~')) {
    return { error: "Only ~ and ~/… are supported, not another user's home (~name)." };
  }
  if (!s.startsWith('/')) return { error: ABSOLUTE_HINT };

  const out: string[] = [];
  for (const part of s.split('/')) {
    if (part === '' || part === '.') continue;
    if (part === '..') out.pop();
    else out.push(part);
  }
  return { path: '/' + out.join('/') };
}

/** Whether path is a root or lies inside one (lexically, ignoring case as APFS does). */
export function withinRoots(path: string, roots: string[]): boolean {
  const p = path.toLowerCase();
  return roots.some((r) => {
    const root = r.toLowerCase().replace(/\/+$/, '');
    return root === '' || p === root || p.startsWith(root + '/');
  });
}
