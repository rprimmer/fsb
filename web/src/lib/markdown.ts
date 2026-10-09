// Markdown to HTML for the preview pane. Pure (no DOM), so it can be tested
// with `node --test`.
//
// This is the first of three layers. The HTML it produces is then sanitized
// with DOMPurify (Preview.svelte) and finally shown in a sandboxed iframe whose
// policy forbids scripts of its own and all network access (see web/mdframe.go).
// Each layer assumes the one before it failed.
//
// Rules applied here:
//  - raw HTML in the source is shown as text, never as markup;
//  - links never carry an href, nor their target at all: the element holds only
//    an index into `links`, so a target never passes through HTML parsing
//    (which would turn the NUL marking a byte that is not UTF-8 into U+FFFD). A
//    relative link becomes a "file" link that the app resolves inside its own
//    guarded API; an http(s) link becomes an "external" link that the app opens
//    in a new tab without a referrer; anything else (javascript:, data:, file:,
//    ...) is shown as plain text;
//  - images are never given a src here. A relative image is marked so the app
//    can fetch it through the guarded preview endpoint; a remote image is
//    replaced by a placeholder, so opening a Markdown file makes no request.

import { Marked, type Tokens } from 'marked';

export interface RenderedMarkdown {
  html: string;
  /** Local images to fetch, by index: the marker is `data-fsb-img="N"`. */
  images: string[];
  /** Link targets, by index: the marker is `data-fsb-link="N"`. */
  links: LinkTarget[];
}

export type LinkTarget =
  | { kind: 'file'; path: string }
  | { kind: 'external'; url: string }
  | { kind: 'none' };

const SCHEME = /^[a-zA-Z][a-zA-Z0-9+.-]*:/;

/** Resolve a link's target against the directory of the Markdown file. */
export function resolveLink(baseDir: string, href: string): LinkTarget {
  const h = href.trim();
  if (h === '' || h.startsWith('#')) return { kind: 'none' };
  if (SCHEME.test(h)) {
    try {
      const u = new URL(h);
      if (u.protocol === 'http:' || u.protocol === 'https:') return { kind: 'external', url: u.href };
    } catch {
      /* fall through */
    }
    return { kind: 'none' };
  }
  if (h.startsWith('//')) return { kind: 'none' }; // scheme-relative: a remote URL in disguise
  let p = h.replace(/[?#].*$/s, '');
  try {
    p = decodeURIComponent(p);
  } catch {
    return { kind: 'none' };
  }
  if (p === '' || p.includes('\0') || p.includes('\\')) return { kind: 'none' };
  const parts = (p.startsWith('/') ? p : baseDir + '/' + p).split('/');
  const out: string[] = [];
  for (const s of parts) {
    if (s === '' || s === '.') continue;
    if (s === '..') out.pop();
    else out.push(s);
  }
  return { kind: 'file', path: '/' + out.join('/') };
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

export function renderMarkdown(source: string, baseDir: string): RenderedMarkdown {
  const images: string[] = [];
  const links: LinkTarget[] = [];
  const md = new Marked({ gfm: true, breaks: false });
  md.use({
    renderer: {
      html({ text }: Tokens.HTML | Tokens.Tag) {
        return esc(text);
      },
      link(this: { parser: { parseInline(t: Tokens.Generic[]): string } }, { href, tokens }: Tokens.Link) {
        const inner = this.parser.parseInline(tokens);
        const t = resolveLink(baseDir, href);
        if (t.kind === 'none') return inner;
        links.push(t);
        return `<a data-fsb="${t.kind}" data-fsb-link="${links.length - 1}" tabindex="0" role="link">${inner}</a>`;
      },
      image({ href, text }: Tokens.Image) {
        const t = resolveLink(baseDir, href);
        if (t.kind === 'file') {
          images.push(t.path);
          return `<img data-fsb-img="${images.length - 1}" alt="${esc(text)}">`;
        }
        const why = t.kind === 'external' ? 'Remote image blocked' : 'Image not shown';
        return `<span class="noimg">${why}${text ? ': ' + esc(text) : ''}</span>`;
      },
    },
  });
  return { html: md.parse(source, { async: false }) as string, images, links };
}

/** Splice fetched images (as data: URLs) into rendered HTML. Unfetched markers become placeholders. */
export function fillImages(html: string, urls: (string | null)[]): string {
  return html.replace(/<img data-fsb-img="(\d+)" alt="([^"]*)">/g, (_m, n: string, alt: string) => {
    const u = urls[Number(n)];
    if (!u || !/^data:image\/(png|jpeg|gif|webp);base64,[A-Za-z0-9+/=]*$/.test(u)) {
      return `<span class="noimg">Image not shown${alt ? ': ' + alt : ''}</span>`;
    }
    return `<img src="${u}" alt="${alt}">`;
  });
}
