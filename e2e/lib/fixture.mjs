import { execFileSync } from 'node:child_process';
import { linkSync, mkdirSync, mkdtempSync, realpathSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import zlib, { deflateSync } from 'node:zlib';

/** A tar archive holding the given files (ustar headers, computed checksums). */
export function makeTar(files) {
  const blocks = [];
  for (const [name, body] of Object.entries(files)) {
    const h = Buffer.alloc(512);
    h.write(name, 0, 100, 'utf8');
    h.write('0000644\0', 100); h.write('0000000\0', 108); h.write('0000000\0', 116);
    h.write(body.length.toString(8).padStart(11, '0') + '\0', 124);
    h.write('00000000000\0', 136);
    h.write('        ', 148); h.write('0', 156); h.write('ustar\0' + '00', 257);
    let sum = 0; for (const b of h) sum += b;
    h.write(sum.toString(8).padStart(6, '0') + '\0 ', 148);
    blocks.push(h, Buffer.from(body), Buffer.alloc((512 - (body.length % 512)) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return Buffer.concat(blocks);
}

/** A zip with stored (uncompressed) entries. */
export function makeZip(files) {
  const parts = [], central = [];
  let off = 0;
  for (const [name, body] of Object.entries(files)) {
    const nm = Buffer.from(name), data = Buffer.from(body), crc = zlib.crc32(data);
    const lh = Buffer.alloc(30);
    lh.writeUInt32LE(0x04034b50, 0); lh.writeUInt16LE(20, 4); lh.writeUInt32LE(crc, 14);
    lh.writeUInt32LE(data.length, 18); lh.writeUInt32LE(data.length, 22); lh.writeUInt16LE(nm.length, 26);
    const ch = Buffer.alloc(46);
    ch.writeUInt32LE(0x02014b50, 0); ch.writeUInt16LE(20, 4); ch.writeUInt16LE(20, 6); ch.writeUInt32LE(crc, 16);
    ch.writeUInt32LE(data.length, 20); ch.writeUInt32LE(data.length, 24); ch.writeUInt16LE(nm.length, 28); ch.writeUInt32LE(off, 42);
    parts.push(lh, nm, data); central.push(ch, nm);
    off += 30 + nm.length + data.length;
  }
  const cd = Buffer.concat(central);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0); end.writeUInt16LE(Object.keys(files).length, 8); end.writeUInt16LE(Object.keys(files).length, 10);
  end.writeUInt32LE(cd.length, 12); end.writeUInt32LE(off, 16);
  return Buffer.concat([...parts, cd, end]);
}

/** A one-page PDF saying "Hello fsb PDF", with a correct cross-reference table. */
export function makePdf() {
  const objs = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 120] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
    (() => { const s = 'BT /F1 24 Tf 20 60 Td (Hello fsb PDF) Tj ET'; return `<< /Length ${s.length} >>\nstream\n${s}\nendstream`; })(),
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ];
  let out = '%PDF-1.4\n';
  const offs = [];
  objs.forEach((o, i) => { offs.push(out.length); out += `${i + 1} 0 obj\n${o}\nendobj\n`; });
  const xref = out.length;
  out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n` + offs.map((o) => String(o).padStart(10, '0') + ' 00000 n \n').join('');
  out += `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(out, 'latin1');
}

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

function mkfifoOrNull(path) {
  try {
    execFileSync('mkfifo', [path], { stdio: 'ignore' });
    return true;
  } catch {
    return false; // no mkfifo on this platform; the test using it will skip
  }
}

function setXattrHex(file, name) {
  try {
    if (process.platform === 'darwin') execFileSync('xattr', ['-wx', name, 'de ad be ef 00 01', file], { stdio: 'ignore' });
    else execFileSync('setfattr', ['-n', `user.${name}`, '-v', '0xdeadbeef0001', file], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
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

/**
 * Names that are not UTF-8, which only Linux can hold (APFS refuses them):
 * Latin-1 "café.txt", and a folder "café" whose notes.md links to its sibling.
 * Returns the file's path as the interface holds it: the byte 0xE9 escaped as
 * NUL and two hex digits (API-10). e2e/lib/container.mjs makes the same names
 * inside a container; keep the two alike.
 */
export function makeNonUtf8(home) {
  const raw = (rest) => Buffer.concat([Buffer.from(join(home, 'spoof', 'caf')), Buffer.from([0xe9]), Buffer.from(rest)]);
  writeFileSync(raw('.txt'), 'bonjour\n');
  mkdirSync(raw(''));
  writeFileSync(raw('/notes.md'), NOTES_MD);
  writeFileSync(raw('/sibling.txt'), 'the sibling\n');
  return join(home, 'spoof', 'caf\u0000E9.txt');
}

/** notes.md in the folder whose name is not UTF-8. */
export const NOTES_MD = '[the sibling](sibling.txt)\n';

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
  // Names that differ only in case (they must be different names: APFS is case-insensitive).
  put(join(home, 'casetest', 'Report-final.txt'), 'x');
  put(join(home, 'casetest', 'report-draft.txt'), 'x');
  // Names that read as something else: a right-to-left override and a newline.
  put(join(home, 'spoof', 'invoice\u202Etxt.exe'), 'x');
  put(join(home, 'spoof', 'two\nlines.txt'), 'x');
  put(join(home, 'spoof', 'שלום.txt'), 'x'); // real right-to-left text is left alone
  // A Markdown link to a name holding a literal %, written %25 as Markdown needs.
  put(join(home, 'spoof', 'links.md'), '[a percent](100%25.txt)\n');
  put(join(home, 'spoof', '100%.txt'), 'one hundred percent\n');

  put(join(work, 'src', 'main.go'), 'package main\n\nimport "fmt"\n\n// greet prints a greeting.\nfunc greet(name string) string { return fmt.Sprintf("hello, %s", name) }\n\nfunc main() { fmt.Println(greet("fsb")) }\n');
  put(join(work, 'src', 'deep', 'er', 'Needle-Deep.txt'), 'needle deep\n');
  put(join(work, 'data.json'), '{"name":"fsb","tags":["a","b"],"nested":{"ok":true,"n":42}}');
  put(join(work, 'people.csv'), 'name,city,note\nAda,London,"likes, commas"\nGrace,New York,"said ""hi"""\n');
  put(join(work, 'README.md'), [
    '# Title', '', 'Some *markdown* with `code`.', '',
    'See [the main file](src/main.go) and [the site](https://example.com/docs).', '',
    '![gradient](../pics/gradient.png)', '',
    '![tracker](https://tracker.invalid/pixel.gif)', '',
    '<script>document.title = "PWNED"</script>', '',
    '[click me](javascript:alert(1))', '',
  ].join('\n'));
  put(join(work, 'doc.pdf'), makePdf());
  put(join(work, 'bundle.zip'), makeZip({ 'pkg/readme.txt': 'SECRET-INSIDE-ZIP', 'pkg/lib/a.go': 'package a\n' }));
  put(join(work, 'bundle.tar'), makeTar({ 'top.txt': 'hello', 'sub/inner.txt': 'SECRET-INSIDE-TAR' }));
  put(join(work, 'fake.zip'), '<html><script>alert(1)</script></html>');
  put(join(work, 'fake.pdf'), '<html><script>alert(1)</script></html>');
  put(join(work, 'app.log'), 'plain log line 1\nplain log line 2\n');
  put(join(work, 'random.bin'), Buffer.from(Array.from({ length: 3000 }, (_, i) => (i * 131 + 7) % 256)));
  put(join(work, 'disguised.png'), '<html><script>alert(1)</script></html>');
  put(join(work, 'needle-shallow.txt'), 'needle shallow\n');
  symlinkSync('src/main.go', join(work, 'link-to-main'));
  // A second name for a denied file, and an ordinary file with two names.
  linkSync(join(work, '.env'), join(work, 'src', 'deep', 'innocent-name.txt'));
  put(join(work, 'src', 'deep', 'er', 'pair-a.dat'), 'ordinary pair\n');
  linkSync(join(work, 'src', 'deep', 'er', 'pair-a.dat'), join(work, 'src', 'deep', 'er', 'pair-b.dat'));

  const png = makePng();
  put(join(home, 'pics', 'gradient.png'), png);
  put(join(home, 'office', 'letter.docx'), makeZip({
    '[Content_Types].xml': '<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>',
    '_rels/.rels': '<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>',
    'word/document.xml': '<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hello from fsb</w:t></w:r></w:p></w:body></w:document>',
  }));
  put(join(home, 'pics', 'no-extension'), png);

  const xattrs = setXattr(join(work, 'data.json'), 'com.example.note', 'hello from an xattr');
  const xattrsHex = xattrs && setXattrHex(join(work, 'data.json'), 'com.example.blob');

  if (big) {
    mkdirSync(join(home, 'big'));
    for (let i = 1; i <= 100_000; i++) writeFileSync(join(home, 'big', `file-${i}`), '');
  }
  // What the home folder lists: .ssh and .aws are denied, node_modules is hidden.
  const homeRows = [...(big ? ['big/'] : []), 'casetest/', 'office/', 'pics/', 'spoof/', 'work/'];

  // A special file: listed like any other entry, but never opened (guard.ErrNotRegular).
  // A pipe, not a socket, so it needs no short-path workaround for sun_path's OS limit.
  mkdirSync(join(work, 'special'), { recursive: true });
  const fifoPath = join(work, 'special', 'a.pipe');
  const hasFifo = mkfifoOrNull(fifoPath);
  const nonUtf8 = process.platform === 'linux' ? makeNonUtf8(home) : null;
  return { root, home, work, xattrs, xattrsHex, homeRows, nonUtf8, fifoPath: hasFifo ? fifoPath : null };
}
