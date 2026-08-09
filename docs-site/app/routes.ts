import { index, route, type RouteConfig } from '@react-router/dev/routes';

export default [
  index('routes/home.tsx'),
  route('docs/*', 'routes/docs.tsx'),
  route('search-index.json', 'routes/search-index.ts'),
] satisfies RouteConfig;
