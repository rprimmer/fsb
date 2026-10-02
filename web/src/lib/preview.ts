// Pure helpers for the preview pane and hover peek. No DOM, no highlight.js, so
// they can be tested with `node --test`.

const IMAGE_EXT = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp']);

/** Extension of a file name in lower case ("" for none; a leading dot is not an extension). */
export function extOf(name: string): string {
  const dot = name.lastIndexOf('.');
  return dot > 0 && dot < name.length - 1 ? name.slice(dot + 1).toLowerCase() : '';
}

/**
 * Whether to ask the server for an inline image preview. Only a hint: the
 * server decides from the file's bytes and refuses anything that is not a
 * PNG, JPEG, GIF or WebP.
 */
export function looksLikeImage(name: string): boolean {
  return IMAGE_EXT.has(extOf(name));
}

/** Whether to try the inline PDF viewer. Only a hint: the server checks the file's bytes. */
export function looksLikePdf(name: string): boolean {
  return extOf(name) === 'pdf';
}

const QUICKLOOK_EXT = new Set(['numbers', 'pages', 'key', 'docx', 'doc', 'xlsx', 'xls', 'pptx', 'ppt']);

/**
 * Whether to ask the server for a Quick Look picture (iWork and Office
 * documents, which may be files or packages). Only a hint: the server decides.
 */
export function looksLikeQuickLook(name: string): boolean {
  return QUICKLOOK_EXT.has(extOf(name));
}

const ARCHIVE_EXT = new Set(['zip', 'jar', 'tar', 'tgz', 'gz']);

/** Whether to try listing this as an archive. Only a hint: the server decides from the file's bytes. */
export function looksLikeArchive(name: string): boolean {
  return ARCHIVE_EXT.has(extOf(name));
}

const LANG_BY_EXT: Record<string, string> = {
  js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'javascript',
  ts: 'typescript', tsx: 'typescript',
  json: 'json', jsonc: 'json', webmanifest: 'json',
  html: 'xml', htm: 'xml', xml: 'xml', svg: 'xml', plist: 'xml', xhtml: 'xml',
  css: 'css',
  md: 'markdown', markdown: 'markdown',
  py: 'python',
  go: 'go',
  rs: 'rust',
  sh: 'bash', bash: 'bash', zsh: 'bash',
  yml: 'yaml', yaml: 'yaml',
  sql: 'sql',
  c: 'c', h: 'c',
  cc: 'cpp', cpp: 'cpp', cxx: 'cpp', hpp: 'cpp', hh: 'cpp',
  java: 'java',
  swift: 'swift',
  toml: 'ini', ini: 'ini', cfg: 'ini', conf: 'ini',
  diff: 'diff', patch: 'diff',
  rb: 'ruby',
  php: 'php',
  kt: 'kotlin',
};

const LANG_BY_NAME: Record<string, string> = {
  makefile: 'makefile',
  dockerfile: 'dockerfile',
  '.zshrc': 'bash',
  '.bashrc': 'bash',
  '.bash_profile': 'bash',
  '.zprofile': 'bash',
  '.gitconfig': 'ini',
  '.editorconfig': 'ini',
};

/** The highlight.js language for a file name, or "" for plain text. */
export function languageFor(name: string): string {
  return LANG_BY_NAME[name.toLowerCase()] ?? LANG_BY_EXT[extOf(name)] ?? '';
}

export type PreviewFormat = 'csv' | 'tsv' | 'json' | 'code' | 'text' | 'markdown';

/** How the preview pane should present a text file. */
export function formatFor(name: string): PreviewFormat {
  const ext = extOf(name);
  if (ext === 'csv') return 'csv';
  if (ext === 'tsv') return 'tsv';
  if (ext === 'json') return 'json';
  if (ext === 'md' || ext === 'markdown') return 'markdown';
  return languageFor(name) ? 'code' : 'text';
}

/**
 * Pretty-prints JSON, or returns null when the text is not (complete) JSON.
 * A truncated head of a big file is not valid JSON, so callers fall back to
 * highlighted source for those.
 */
export function prettyJSON(text: string): string | null {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return null;
  }
}

/**
 * A small RFC 4180-style parser: quoted fields, doubled quotes, embedded
 * delimiters and newlines inside quotes, CRLF or LF. Stops after maxRows rows.
 * Returns the rows and whether the input had more.
 */
export function parseDelimited(
  text: string,
  delimiter: string,
  maxRows: number,
): { rows: string[][]; more: boolean } {
  const rows: string[][] = [];
  let row: string[] = [];
  let field = '';
  let inQuotes = false;
  let i = 0;
  const endField = () => {
    row.push(field);
    field = '';
  };
  const endRow = () => {
    endField();
    rows.push(row);
    row = [];
  };
  while (i < text.length) {
    const c = text[i];
    if (inQuotes) {
      if (c === '"') {
        if (text[i + 1] === '"') {
          field += '"';
          i += 2;
          continue;
        }
        inQuotes = false;
      } else {
        field += c;
      }
    } else if (c === '"' && field === '') {
      inQuotes = true;
    } else if (c === delimiter) {
      endField();
    } else if (c === '\n' || c === '\r') {
      if (c === '\r' && text[i + 1] === '\n') i++;
      endRow();
      if (rows.length >= maxRows) return { rows, more: i + 1 < text.length };
    } else {
      field += c;
    }
    i++;
  }
  if (field !== '' || row.length > 0) endRow();
  return { rows, more: false };
}

/** The first `maxLines` lines of text, capped at `maxChars`; says whether anything was cut. */
export function firstLines(text: string, maxLines: number, maxChars: number): { text: string; cut: boolean } {
  let end = 0;
  let lines = 0;
  while (end < text.length && lines < maxLines) {
    const nl = text.indexOf('\n', end);
    if (nl < 0) {
      end = text.length;
      break;
    }
    end = nl + 1;
    lines++;
  }
  let out = text.slice(0, end);
  let cut = end < text.length;
  if (out.length > maxChars) {
    out = out.slice(0, maxChars);
    // Do not end on half of a surrogate pair.
    const last = out.charCodeAt(out.length - 1);
    if (last >= 0xd800 && last <= 0xdbff) out = out.slice(0, -1);
    cut = true;
  }
  return { text: out.replace(/\n$/, ''), cut };
}

/** "3 KB", "1.5 MB" - reuses the same rounding as the listing. */
export function plural(n: number, one: string, many = one + 's'): string {
  return `${n.toLocaleString()} ${n === 1 ? one : many}`;
}
