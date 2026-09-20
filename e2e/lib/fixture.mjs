import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, realpathSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { deflateSync } from 'node:zlib';

/** A 60x40 RGB gradient PNG built with the standard library only. */
export function makePng() {
  const w = 60;
  const h = 40;
  const raw = Buffer.alloc((w * 3 + 1) * h);
  for (let y = 0; y < h; y++) {
    const row = y * (w * 3 + 1);
    for (let x = 0; x < w; x++) {
      raw[row + 1 + x * 3] = (x * 4) % 256;
      raw[row + 2 + x * 3] = (y * 6) % 256;
      raw[row + 3 + x * 3] = 160;
    }
  }
  const crcTable = Array.from({ length: 256 }, (_, n) => {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    return c >>> 0;
  });
  const crc = (buf) => {
    let c = 0xffffffff;
    for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  };
  const chunk = (type, data) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const td = Buffer.concat([Buffer.from(type), data]);
    const c = Buffer.alloc(4);
    c.writeUInt32BE(crc(td));
    return Buffer.concat([len, td, c]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 2; // RGB
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

function setXattr(file, name, value) {
  try {
    if (process.platform === 'darwin') execFileSync('xattr', ['-w', name, value, file], { stdio: 'ignore' });
    else execFileSync('setfattr', ['-n', `user.${name}`, '-v', value, file], { stdio: 'ignore' });
    return true;
  } catch {
    return false; // no tool, or the filesystem does not support user attributes
  }
}

const put = (path, content) => {
  mkdirSync(join(path, '..'), { recursive: true });
  writeFileSync(path, content);
};

/**
 * Builds a fake home directory. Everything the tests assert about is here:
 * secrets that must be denied, files that must be hidden, one file of each
 * previewable type, a symlink, a disguised file, and a deep tree for search.
 *
 * Set big=true to add a 100,000-file folder for the scale test.
 */
export function makeFixture({ big = false } = {}) {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'fsb-e2e-')));
  const home = join(root, 'home');
  const work = join(home, 'work');

  // Denied by the core rules: must never be listed, read, previewed or searched.
  put(join(home, '.ssh', 'id_test'), 'SECRET-KEY-MATERIAL\n');
  put(join(home, '.aws', 'credentials'), 'SECRET-AWS\n');
  put(join(work, '.env'), 'MYSECRET=hunter2\n');
  put(join(work, 'server.pem'), 'SECRET-PEM\n');
  put(join(work, 'id.key'), 'SECRET-KEY\n');
  // Hidden by the default ignore rules, but reachable by path.
  put(join(home, 'node_modules', 'pkg', 'needle.js'), 'hidden needle\n');
  // A dotfile that is neither denied nor ignored: shown only with "Hidden files".
  put(join(work, '.envrc'), 'export FOO=1\n');

  put(join(work, 'src', 'main.go'), 'package main\n\nimport "fmt"\n\n// greet prints a greeting.\nfunc greet(name string) string { return fmt.Sprintf("hello, %s", name) }\n\nfunc main() { fmt.Println(greet("fsb")) }\n');
  put(join(work, 'src', 'deep', 'er', 'Needle-Deep.txt'), 'needle deep\n');
  put(join(work, 'data.json'), '{"name":"fsb","tags":["a","b"],"nested":{"ok":true,"n":42}}');
  put(join(work, 'people.csv'), 'name,city,note\nAda,London,"likes, commas"\nGrace,New York,"said ""hi"""\n');
  put(join(work, 'README.md'), '# Title\n\nSome *markdown*.\n');
  put(join(work, 'app.log'), 'plain log line 1\nplain log line 2\n');
  put(join(work, 'random.bin'), Buffer.from(Array.from({ length: 3000 }, (_, i) => (i * 131 + 7) % 256)));
  put(join(work, 'disguised.png'), '<html><script>alert(1)</script></html>');
  put(join(work, 'needle-shallow.txt'), 'needle shallow\n');
  symlinkSync('src/main.go', join(work, 'link-to-main'));

  const png = makePng();
  put(join(home, 'pics', 'gradient.png'), png);
  put(join(home, 'pics', 'no-extension'), png);

  const xattrs = setXattr(join(work, 'data.json'), 'com.example.note', 'hello from an xattr');

  if (big) {
    mkdirSync(join(home, 'big'));
    for (let i = 1; i <= 100_000; i++) writeFileSync(join(home, 'big', `file-${i}`), '');
  }
  // What the home folder lists: .ssh and .aws are denied, node_modules is hidden.
  const homeRows = [...(big ? ['big/'] : []), 'pics/', 'work/'];
  return { root, home, work, xattrs, homeRows };
}
