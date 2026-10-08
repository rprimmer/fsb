// Runs fsb in a Linux container for the browser suites, so that Chrome or
// Safari on the Mac drives an fsb running on Linux (FSB_E2E_LINUX=debian, ...).
// The fixture is built on the Mac as usual, then copied into the container at
// the same absolute path, so every path the tests use is valid on both sides.
// fsb listens on 127.0.0.1 in the container, which shares the Docker virtual
// machine's network; Colima forwards that loopback port to the Mac's (see
// e2e/linux/browse.sh).
import { execFileSync, spawn } from 'node:child_process';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { sleep } from './util.mjs';

const repoRoot = fileURLToPath(new URL('../../', import.meta.url));

// The distributions of e2e/linux/run.sh: image and platform.
const DISTROS = {
  debian: ['debian:trixie', 'linux/arm64'],
  ubuntu: ['ubuntu:24.04', 'linux/arm64'],
  fedora: ['fedora:latest', 'linux/arm64'],
  alpine: ['alpine:latest', 'linux/arm64'],
  arch: ['archlinux', 'linux/amd64'],
};

const docker = (args, opts = {}) => execFileSync('docker', args, { encoding: 'utf8', ...opts });

/**
 * Starts fsb on Linux distribution `name`, serving fx.home, and returns what
 * server.mjs's startServer returns, plus `platform: 'linux'`. Adds to fx what
 * only Linux can hold: `xattrs`/`xattrsHex` (set with setfattr) and `nonUtf8`,
 * the path of a file in spoof/ whose name is not UTF-8.
 */
export async function startContainerServer(name, fx) {
  const distro = DISTROS[name];
  if (!distro) throw new Error(`FSB_E2E_LINUX=${name}: not one of ${Object.keys(DISTROS).join(', ')}`);
  const [image, platform] = distro;
  if (platform === 'linux/amd64' && process.arch === 'arm64') {
    // Seen 2026-10-08: under QEMU's amd64 emulation, fsb (like any Go program)
    // dies under the suite's load with garbage-collector errors such as "found
    // pointer to free object", on Arch and on an amd64 Debian alike, while the
    // same distributions pass natively.  The failures are the emulator's.
    console.log(`# warning: ${name} is amd64 and runs emulated here; Go programs crash under QEMU's emulation, so failures say nothing about fsb`);
  }
  const tag = `fsb-smoke:${name}`;
  docker(['build', '-q', '-f', 'e2e/linux/Dockerfile', '--target', 'smoke', '--platform', platform,
    '--build-arg', `BASE=${image}`, '-t', tag, '.'], { cwd: repoRoot, stdio: ['ignore', 'ignore', 'inherit'] });

  const container = `fsb-e2e-${name}`;
  try { docker(['rm', '-f', container], { stdio: 'ignore' }); } catch {}
  // tmpfs: the containers' overlay file system refuses user extended attributes.
  docker(['run', '-d', '--rm', '--init', '--network', 'host', '--platform', platform, '--name', container,
    '--tmpfs', `${fx.root}:rw,exec,mode=1777`, '--entrypoint', 'sleep', tag, 'infinity'], { stdio: 'ignore' });
  const exec = (args, opts) => docker(['exec', '-u', 'tester', container, ...args], opts);
  // FSB_E2E_KEEP_CONTAINER=1 leaves the container running after the suite, to inspect.
  const stop = async () => {
    if (process.env.FSB_E2E_KEEP_CONTAINER) return;
    try { docker(['rm', '-f', container], { stdio: 'ignore' }); } catch {}
  };

  try {
    // Copy the fixture: symbolic links, hard links and the FIFO travel in the
    // archive; macOS's own metadata (AppleDouble files, attributes) does not.
    const pack = spawn('tar', ['--no-xattrs', '--no-mac-metadata', '-C', fx.root, '-cf', '-', 'home'],
      { env: { ...process.env, COPYFILE_DISABLE: '1' }, stdio: ['ignore', 'pipe', 'inherit'] });
    const unpack = spawn('docker', ['exec', '-i', '-u', 'tester', container, 'tar', '-xf', '-', '-C', fx.root],
      { stdio: ['pipe', 'inherit', 'inherit'] });
    pack.stdout.pipe(unpack.stdin);
    const code = await new Promise((resolve) => unpack.on('exit', resolve));
    if (code !== 0) throw new Error(`copying the fixture into ${container} failed (${code})`);

    const data = join(fx.work, 'data.json');
    const setx = (...args) => { try { exec(['setfattr', ...args], { stdio: 'ignore' }); return true; } catch { return false; } };
    fx.xattrs = setx('-n', 'user.com.example.note', '-v', 'hello from an xattr', data);
    fx.xattrsHex = fx.xattrs && setx('-n', 'user.com.example.blob', '-v', '0xdeadbeef0001', data);
    // Latin-1 "café.txt": byte 0xE9 is not UTF-8.  fx.nonUtf8 is its path as the
    // interface holds it: the byte escaped as NUL and two hex digits (API-10).
    fx.nonUtf8 = join(fx.home, 'spoof', 'caf\u0000E9.txt');
    exec(['sh', '-c', `printf 'bonjour\\n' > "$(printf '%s/spoof/caf\\351.txt' "$1")"`, 'sh', fx.home]);

    // Started as on the Mac, without --port: fsb picks a port and remembers it in
    // ~/.config/fsb, which the listing tests expect to see.  Colima forwards
    // whichever port it is.
    docker(['exec', '-d', '-u', 'tester', container, 'sh', '-c', 'HOME="$1" exec fsb --no-open > "$1/../fsb.log" 2>&1',
      'sh', fx.home]);
    const log = () => { try { return exec(['cat', join(fx.root, 'fsb.log')]); } catch { return ''; } };
    let m;
    for (let i = 0; i < 60 && !m; i++) {
      await sleep(500);
      m = log().match(/http:\/\/127\.0\.0\.1:(\d+)(\/[A-Za-z0-9_-]+)\/\?token=\S+/);
    }
    if (!m) throw new Error(`fsb did not start in ${container}:\n${log()}`);
    const origin = `http://127.0.0.1:${m[1]}`;
    // Colima notices the new port within a second or two.
    for (let i = 0; i < 40; i++) {
      try { if ((await fetch(origin + '/')).status) break; } catch {}
      await sleep(250);
    }
    const release = docker(['exec', container, 'sh', '-c', '. /etc/os-release; echo "$PRETTY_NAME"']).trim();
    return { url: m[0], origin, base: origin + m[2], stderr: log, stop, platform: 'linux', release };
  } catch (e) {
    await stop();
    throw e;
  }
}
