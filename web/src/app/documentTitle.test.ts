import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('document title', () => {
  it('uses the Aurora AIOps product name', () => {
    const html = readFileSync(resolve(process.cwd(), 'index.html'), 'utf8');
    expect(html).toContain('<title>Aurora AIOps</title>');
    expect(html.toLowerCase()).not.toContain('<title>kubejojo</title>');
  });
});
