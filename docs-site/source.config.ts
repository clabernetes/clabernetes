import { defineConfig } from 'fumadocs-mdx/config';
import { remarkSteps } from 'fumadocs-core/mdx-plugins/remark-steps';

export default defineConfig({
    mdxOptions: {
        remarkPlugins: [remarkSteps],
    },
});