import { defineSuite } from './suite.mjs';
import { launchChrome } from './lib/chrome.mjs';

defineSuite({ label: 'Chrome', launch: () => launchChrome() });
