import { unified } from 'unified';
import remarkParse from 'remark-parse';
import remarkRehype from 'remark-rehype';
import rehypeStringify from 'rehype-stringify';

const processor = unified()
  .use(remarkParse)
  .use(remarkRehype, { allowDangerousHtml: false })
  .use(rehypeStringify);

/** Render CRD description text (markdown) to sanitized HTML. */
export function renderDescriptionMarkdown(text: string): string {
  return String(processor.processSync(text)).trim();
}
