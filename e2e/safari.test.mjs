// Runs the browser suite in Safari. Requires macOS and Safari's "Allow remote
// automation" setting (see lib/safari.mjs and README.md). When Safari cannot be
// automated it reports one clear skip with the reason, rather than failing
// every test for the same setup problem.
import { describe, it } from 'node:test';
import { launchSafari } from './lib/safari.mjs';
import { defineSuite } from './suite.mjs';

let unavailable = '';
let driver;
if (process.platform !== 'darwin') unavailable = 'Safari is only available on macOS';
else if (process.env.FSB_E2E_SKIP_SAFARI) unavailable = 'skipped by FSB_E2E_SKIP_SAFARI';
else {
  try {
    driver = await launchSafari();
  } catch (e) {
    unavailable = `${e.message} (see e2e/README.md, "Safari")`;
  }
}

if (driver) defineSuite({ label: 'Safari', launch: async () => driver });
else describe('Safari', () => it('cannot be automated here', (t) => t.skip(unavailable)));
