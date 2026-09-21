// Where the application was loaded from. Pure, so it can be tested without a browser.

/**
 * fsb is served under a random per-launch prefix, "/<prefix>/", which is also the
 * path its session cookie is scoped to (browsers never scope a cookie by port).
 * Every request is therefore made relative to the page's own directory, never
 * to the site root.
 */
export function apiBase(pathname: string): string {
  return pathname.replace(/[^/]*$/, '');
}

/** The URL of an endpoint ("api/list?...") under the prefix this page was loaded from. */
export function apiPath(rest: string): string {
  return apiBase(typeof location === 'undefined' ? '/' : location.pathname) + rest;
}
