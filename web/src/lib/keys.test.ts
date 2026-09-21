import assert from 'node:assert/strict';
import { test } from 'node:test';
import { keyOf } from './keys.ts';

test('a normal key event maps to itself', () => {
  assert.equal(keyOf({ key: 'ArrowLeft', code: 'ArrowLeft' }), 'ArrowLeft');
  assert.equal(keyOf({ key: 'Enter', code: 'Enter' }), 'Enter');
  assert.equal(keyOf({ key: 'Escape', code: 'Escape' }), 'Escape');
  assert.equal(keyOf({ key: 'Backspace', code: 'Backspace' }), 'Backspace');
});

test('navigation keys are recognized by code when a modifier changes key (Safari under WebDriver)', () => {
  // Captured from Safari 27: Alt+ArrowLeft arrived as key "" with the right code.
  assert.equal(keyOf({ key: '', code: 'ArrowLeft' }), 'ArrowLeft');
  assert.equal(keyOf({ key: '', code: 'ArrowRight' }), 'ArrowRight');
  assert.equal(keyOf({ key: '', code: 'ArrowUp' }), 'ArrowUp');
  assert.equal(keyOf({ key: '', code: 'ArrowDown' }), 'ArrowDown');
  assert.equal(keyOf({ key: '', code: 'Home' }), 'Home');
  assert.equal(keyOf({ key: 'x', code: 'PageDown' }), 'PageDown');
});

test('Space is recognized by code, since some layers report an empty key', () => {
  assert.equal(keyOf({ key: ' ', code: 'Space' }), ' ');
  assert.equal(keyOf({ key: '', code: 'Space' }), ' ');
});

test('letters and punctuation keep key, so the keyboard layout is respected', () => {
  assert.equal(keyOf({ key: 'c', code: 'KeyC' }), 'c');
  assert.equal(keyOf({ key: '/', code: 'Slash' }), '/');
  // On a Dvorak layout the physical "KeyJ" produces "c": the shortcut follows the character.
  assert.equal(keyOf({ key: 'c', code: 'KeyJ' }), 'c');
  assert.equal(keyOf({ key: 's', code: 'KeyS' }), 's');
});

test('unknown codes fall back to key', () => {
  assert.equal(keyOf({ key: 'Tab', code: 'Tab' }), 'Tab');
  assert.equal(keyOf({ key: 'Unidentified', code: '' }), 'Unidentified');
});
