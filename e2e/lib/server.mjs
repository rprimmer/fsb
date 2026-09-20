import { execFileSync, spawn } from 'node:child_process';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = fileURLToPath(new URL('../../', import.meta.url));

/** Builds fsb (or uses $FSB_BIN) and returns the binary path. */
export function buildFsb(outDir) {
  if (process.env.FSB_BIN) return process.env.FSB_BIN;
  const bin = join(outDir, 'fsb');
  execFileSync('go', ['build', '-o', bin, './cmd/fsb'], { cwd: repoRoot, stdio: 'inherit' });
  return bin;
}

/**
 * Starts fsb with HOME set to the fixture, so the built-in core deny rules and
 * default hide rules apply exactly as they do for a real user with no config.
 */
export function startServer(bin, home) {
  return new Promise((resolve, reject) => {
    const child = spawn(bin, ['--no-open'], { env: { ...process.env, HOME: home }, stdio: ['ignore', 'pipe', 'pipe'] });
    let out = '';
    let err = '';
    const timer = setTimeout(() => reject(new Error(`fsb did not start in 15s.\nstdout: ${out}\nstderr: ${err}`)), 15000);
    child.stderr.on('data', (d) => (err += d));
    child.stdout.on('data', (d) => {
      out += d;
      const m = out.match(/http:\/\/127\.0\.0\.1:(\d+)\/\?token=\S+/);
      if (m) {
        clearTimeout(timer);
        resolve({
          url: m[0], // single-use launch URL
          base: `http://127.0.0.1:${m[1]}`,
          stderr: () => err,
          stop: () => new Promise((r) => { child.once('exit', r); child.kill(); }),
        });
      }
    });
    child.on('exit', (code) => {
      clearTimeout(timer);
      reject(new Error(`fsb exited early (${code}).\nstdout: ${out}\nstderr: ${err}`));
    });
  });
}
