import { createBrowserRouter } from 'react-router';
import type { RouteObject } from 'react-router';
import AppShell from '../components/AppShell';
import Forbidden from '../views/Forbidden';
import Home from '../views/Home';
import Login from '../views/Login';
import NotFound from '../views/NotFound';
import { publicOnly, requirePermissions, requireSession } from './guards';

/**
 * Route table for the React stack. Permission requirements are declared per
 * route so a loader for a nested branch inherits the parent's check by running
 * after it, which is how the Vue guard's merged `meta.requiredPermissions`
 * behaved. W11-D appends the new model pages here.
 */
export const appRoutes: RouteObject[] = [
  {
    path: '/',
    element: <AppShell />,
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
