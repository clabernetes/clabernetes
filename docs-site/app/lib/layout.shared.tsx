import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared';

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: <span className="c9s-gradient-text">c9s</span>,
      url: '/',
    },
    links: [
      {
        text: 'Documentation',
        url: '/docs',
        active: 'nested-url',
        on: 'nav',
      },
    ],
    githubUrl: 'https://github.com/clabernetes/clabernetes',
  };
}
