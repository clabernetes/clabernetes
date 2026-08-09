import path from 'node:path';
import { fileURLToPath } from 'node:url';

/** Repository root (parent of docs-site/). */
export function getRepoRoot(): string {
  return path.resolve(
    fileURLToPath(new URL('../../../../', import.meta.url)),
  );
}
