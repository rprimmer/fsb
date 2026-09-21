import { createServer } from 'node:http';

/**
 * A stand-in for a malicious website on another origin. It serves one blank
 * page; the tests then run script "as the attacker" in that page's origin (via
 * the driver) to try to reach fsb with the visitor's browser session.
 *
 * It is reached as http://localhost:PORT, while fsb is at http://127.0.0.1:PORT:
 * a different host, so a different origin and a different site.
 */
export function startAttacker() {
  return new Promise((resolve) => {
    const cookies = []; // every Cookie header this server has been sent
    const srv = createServer((req, res) => {
      cookies.push(req.headers.cookie ?? '');
      res.setHeader('Content-Type', 'text/html; charset=utf-8');
      res.end('<!doctype html><title>attacker</title><body>attacker page</body>');
    });
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      resolve({
        url: `http://localhost:${port}/`,
        // The same server at fsb's own host but another port: what another local
        // web server looks like to a browser that holds an fsb session cookie.
        sameHostUrl: `http://127.0.0.1:${port}/`,
        seenCookies: () => [...cookies],
        close: () => new Promise((r) => srv.close(r)),
      });
    });
  });
}
