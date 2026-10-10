import { Navigate, createBrowserRouter } from 'react-router'
import AppLayout from '../components/layout/AppLayout'
import RequireAdmin from '../components/layout/RequireAdmin'
import Dashboard from '../pages/Dashboard'
import Settings from '../pages/Settings'
import NotFound from '../pages/NotFound'
import OrdersList from '../pages/orders/OrdersList'
import OrderLayout from '../pages/orders/OrderLayout'
import OrderOverview from '../pages/orders/OrderOverview'
import OrderPreparations from '../pages/orders/OrderPreparations'
import OrderServings from '../pages/orders/OrderServings'
import OrderFiles from '../pages/orders/OrderFiles'
import OrderEditor from '../pages/orders/OrderEditor'
import PreparationsList from '../pages/preparations/PreparationsList'
import PreparationLayout from '../pages/preparations/PreparationLayout'
import PreparationOverview from '../pages/preparations/PreparationOverview'
import PreparationFiles from '../pages/preparations/PreparationFiles'
import PreparationChanges from '../pages/preparations/PreparationChanges'
import PreparationApprovals from '../pages/preparations/PreparationApprovals'
import ServingsList from '../pages/servings/ServingsList'
import ServingDetail from '../pages/servings/ServingDetail'
import MenusList from '../pages/menus/MenusList'
import MenuLayout from '../pages/menus/MenuLayout'
import MenuOverview from '../pages/menus/MenuOverview'
import MenuOrders from '../pages/menus/MenuOrders'
import MenuEditor from '../pages/menus/MenuEditor'
import PantriesList from '../pages/pantries/PantriesList'
import PantryLayout from '../pages/pantries/PantryLayout'
import PantryOverview from '../pages/pantries/PantryOverview'
import PantryTags from '../pages/pantries/PantryTags'
import PantryEditor from '../pages/pantries/PantryEditor'

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, element: <Dashboard /> },

      { path: 'orders', element: <OrdersList /> },
      { path: 'orders/new', element: <OrderEditor /> },
      { path: 'orders/:namespace/:name/edit', element: <OrderEditor /> },
      {
        path: 'orders/:namespace/:name',
        element: <OrderLayout />,
        children: [
          { index: true, element: <OrderOverview /> },
          { path: 'preparations', element: <OrderPreparations /> },
          { path: 'servings', element: <OrderServings /> },
          { path: 'files', element: <OrderFiles /> },
        ],
      },

      { path: 'preparations', element: <PreparationsList /> },
      {
        path: 'preparations/:namespace/:name',
        element: <PreparationLayout />,
        children: [
          { index: true, element: <PreparationOverview /> },
          { path: 'files', element: <PreparationFiles /> },
          { path: 'changes', element: <PreparationChanges /> },
          { path: 'approvals', element: <PreparationApprovals /> },
        ],
      },

      { path: 'servings', element: <ServingsList /> },
      { path: 'servings/:namespace/:name', element: <ServingDetail /> },

      { path: 'menus', element: <MenusList /> },
      { path: 'menus/new', element: <MenuEditor /> },
      { path: 'menus/:namespace/:name/edit', element: <MenuEditor /> },
      {
        path: 'menus/:namespace/:name',
        element: <MenuLayout />,
        children: [
          { index: true, element: <MenuOverview /> },
          { path: 'orders', element: <MenuOrders /> },
        ],
      },

      { path: 'pantries', element: <PantriesList /> },
      { path: 'pantries/new', element: <PantryEditor /> },
      { path: 'pantries/:namespace/:name/edit', element: <PantryEditor /> },
      {
        path: 'pantries/:namespace/:name',
        element: <PantryLayout />,
        children: [
          { index: true, element: <PantryOverview /> },
          { path: 'tags', element: <PantryTags /> },
        ],
      },

      { path: 'settings', element: <Navigate to="/settings/general" replace /> },
      { path: 'settings/:tab', element: <RequireAdmin><Settings /></RequireAdmin> },

      { path: '*', element: <NotFound /> },
    ],
  },
])
