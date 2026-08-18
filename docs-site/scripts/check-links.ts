import { access, readFile, readdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Link, Root } from 'mdast';
import remarkMdx from 'remark-mdx';
import remarkParse from 'remark-parse';
import { unified } from 'unified';
import { visit } from 'unist-util-visit';
import { rewriteMarkdownFileHref } from '../app/lib/docs-href';

const docsDirectory = fileURLToPath(new URL('../../docs', import.meta.url));
const markdownExtensions = new Set(['.md', '.mdx']);

async function collectMarkdownFiles(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(
    entries.map(async (entry) => {
      const entryPath = path.join(directory, entry.name);

      if (entry.isDirectory()) {
        return collectMarkdownFiles(entryPath);
      }

      return markdownExtensions.has(path.extname(entry.name))
        ? [entryPath]
        : [];
    }),
  );

  return files.flat();
}

function toPosix(value: string): string {
  return value.split(path.sep).join('/');
}

function toRoute(file: string): string {
  const relative = toPosix(path.relative(docsDirectory, file));
  const stem = relative.replace(/\.(md|mdx)$/, '');

  if (stem === 'index') {
    return '/docs';
  }

  if (stem.endsWith('/index')) {
    return `/docs/${stem.slice(0, -'/index'.length)}`;
  }

  return `/docs/${stem}`;
}

function normalizeRoute(route: string): string {
  if (route === '/') {
    return route;
  }

  return route.replace(/\/+$/, '');
}

function stripQueryAndHash(url: string): string {
  return url.split(/[?#]/, 1)[0] ?? '';
}

function isExternal(url: string): boolean {
  return /^(?:[a-z][a-z\d+.-]*:|\/\/)/i.test(url);
}

async function exists(file: string): Promise<boolean> {
  try {
    await access(file);
    return true;
  } catch {
    return false;
  }
}

async function validateRelativeTarget(
  sourceFile: string,
  target: string,
  markdownFiles: Set<string>,
): Promise<string | null> {
  const resolved = path.resolve(path.dirname(sourceFile), target);
  const relativeToDocs = path.relative(docsDirectory, resolved);

  if (
    relativeToDocs === '..' ||
    relativeToDocs.startsWith(`..${path.sep}`) ||
    path.isAbsolute(relativeToDocs)
  ) {
    return 'relative links outside docs/ must use an explicit repository URL';
  }

  if (markdownExtensions.has(path.extname(resolved))) {
    return markdownFiles.has(resolved) ? null : 'target document does not exist';
  }

  if (path.extname(resolved)) {
    return (await exists(resolved)) ? null : 'target asset does not exist';
  }

  const candidates = [
    resolved,
    `${resolved}.md`,
    `${resolved}.mdx`,
    path.join(resolved, 'index.md'),
    path.join(resolved, 'index.mdx'),
  ];

  for (const candidate of candidates) {
    if (await exists(candidate)) {
      return null;
    }
  }

  return 'target does not exist';
}

const files = await collectMarkdownFiles(docsDirectory);
const markdownFiles = new Set(files);
const routes = new Set(['/', ...files.map(toRoute).map(normalizeRoute)]);
const errors: string[] = [];

for (const file of files) {
  const content = await readFile(file, 'utf8');
  const tree = unified().use(remarkParse).use(remarkMdx).parse(content) as Root;

  const links: Link[] = [];
  visit(tree, 'link', (node) => {
    links.push(node);
  });

  for (const link of links) {
    const rawTarget = link.url.trim();

    if (!rawTarget || rawTarget.startsWith('#') || isExternal(rawTarget)) {
      continue;
    }

    const target = decodeURIComponent(stripQueryAndHash(rawTarget));
    let problem: string | null = null;

    if (target.startsWith('/')) {
      problem = routes.has(
        normalizeRoute(rewriteMarkdownFileHref(target)),
      )
        ? null
        : 'site route does not exist';
    } else {
      problem = await validateRelativeTarget(file, target, markdownFiles);
    }

    if (problem) {
      const relativeFile = toPosix(path.relative(docsDirectory, file));
      const line = link.position?.start.line ?? '?';
      errors.push(`${relativeFile}:${line}: ${rawTarget} (${problem})`);
    }
  }
}

if (errors.length > 0) {
  console.error('Documentation link validation failed:\n');
  for (const error of errors) {
    console.error(`- ${error}`);
  }
  process.exitCode = 1;
} else {
  console.log(`Validated links in ${files.length} documentation files.`);
}
