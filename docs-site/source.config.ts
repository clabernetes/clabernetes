import { defineConfig } from 'fumadocs-mdx/config';
import { remarkSteps } from 'fumadocs-core/mdx-plugins/remark-steps';
import { remarkMarkdownFileLinks } from './app/lib/remark-markdown-file-links';

export default defineConfig({
    mdxOptions: {
        remarkPlugins: [remarkSteps, remarkMarkdownFileLinks],
    },
});