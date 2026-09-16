import { createBrowserRouter } from 'react-router';
import type { RouteObject } from 'react-router';
import MainLayout from '../components/MainLayout';
import Forbidden from '../views/Forbidden';
import Home from '../views/Home';
import Login from '../views/Login';
import NotFound from '../views/NotFound';
import { publicOnly, requirePermissions, requireSession } from './guards';

/**
 * Route table for the React stack. Permission requirements are declared per
 * route so a loader for a nested branch inherits the parent's check by running
 * after it, which is how the Vue guard's merged `meta.requiredPermissions`
 * behaved. Each migration batch appends its routes here and removes the
 * corresponding entry from routes/migration.ts.
 */
export const appRoutes: RouteObject[] = [
  {
    path: '/',
    element: <MainLayout />,
    loader: requireSession,
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
  },
  {
    path: '*',
    element: <NotFound />,
  },
];

export const createAppRouter = () => createBrowserRouter(appRoutes);

export { requirePermissions };
