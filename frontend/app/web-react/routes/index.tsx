import { createBrowserRouter, replace } from 'react-router';
import type { RouteObject } from 'react-router';
import MainLayout from '../components/MainLayout';
import Forbidden from '../views/Forbidden';
import Home from '../views/Home';
import Login from '../views/Login';
import NotFound from '../views/NotFound';
import RouteFallback from '../components/RouteFallback';
import { publicOnly, requirePermissions, requireSession } from './guards';

/**
 * Route table for the React stack. Parent and child loaders may run in parallel;
 * protected data loaders must compose `requirePermissions(required, load)` so
 * their own request starts only after authorization. Each migration batch
 * appends its routes here and updates routes/migration.ts.
 */
export const appRoutes: RouteObject[] = [
  {
    path: '/',
    element: <MainLayout />,
    loader: requireSession,
    HydrateFallback: RouteFallback,
    children: [
      { index: true, element: <Home /> },
      {
        path: 'forbidden',
        element: <Forbidden />,
      },
    ],
  },
  {
    path: '/login',
    element: <Login />,
    loader: publicOnly,
    HydrateFallback: RouteFallback,
  },
  {
    path: '/react.html',
    loader: () => replace('/'),
    HydrateFallback: RouteFallback,
  },
  {
    path: '*',
    element: <NotFound />,
  },
];

export const createAppRouter = () => createBrowserRouter(appRoutes);

export { requirePermissions };
