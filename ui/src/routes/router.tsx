import { Navigate, createBrowserRouter } from 'react-router'
import AppLayout from '../components/layout/AppLayout'
import RequireCapability from '../components/layout/RequireCapability'
import Dashboard from '../pages/Dashboard'
import Settings from '../pages/Settings'
import NotFound from '../pages/NotFound'
import ProductsList from '../pages/products/ProductsList'
import ProductLayout from '../pages/products/ProductLayout'
import ProductOverview from '../pages/products/ProductOverview'
import ProductOrders from '../pages/products/ProductOrders'
import ProductPreview from '../pages/products/ProductPreview'
import ProductEditor from '../pages/products/ProductEditor'
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
import PreparationIntent from '../pages/preparations/PreparationIntent'
import PreparationAttestations from '../pages/preparations/PreparationAttestations'
import ServingsList from '../pages/servings/ServingsList'
import ServingDetail from '../pages/servings/ServingDetail'
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

      { path: 'products', element: <ProductsList /> },
      { path: 'products/new', element: <RequireCapability anyOf={['products:create']}><ProductEditor /></RequireCapability> },
      { path: 'products/:namespace/:name/edit', element: <RequireCapability anyOf={['products:update']}><ProductEditor /></RequireCapability> },
      {
        path: 'products/:namespace/:name',
        element: <ProductLayout />,
        children: [
          { index: true, element: <ProductOverview /> },
          { path: 'orders', element: <ProductOrders /> },
          { path: 'preview', element: <ProductPreview /> },
        ],
      },

      { path: 'orders', element: <OrdersList /> },
      { path: 'orders/new', element: <RequireCapability anyOf={['orders:create']}><OrderEditor /></RequireCapability> },
      { path: 'orders/:namespace/:name/edit', element: <RequireCapability anyOf={['orders:update']}><OrderEditor /></RequireCapability> },
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
          { path: 'intent', element: <PreparationIntent /> },
          { path: 'attestations', element: <PreparationAttestations /> },
          { path: 'approvals', element: <PreparationApprovals /> },
        ],
      },

      { path: 'servings', element: <ServingsList /> },
      { path: 'servings/:namespace/:name', element: <ServingDetail /> },

      { path: 'pantries', element: <PantriesList /> },
      { path: 'pantries/new', element: <RequireCapability anyOf={['pantries:create']}><PantryEditor /></RequireCapability> },
      { path: 'pantries/:namespace/:name/edit', element: <RequireCapability anyOf={['pantries:update']}><PantryEditor /></RequireCapability> },
      {
        path: 'pantries/:namespace/:name',
        element: <PantryLayout />,
        children: [
          { index: true, element: <PantryOverview /> },
          { path: 'tags', element: <PantryTags /> },
        ],
      },

      { path: 'settings', element: <Navigate to="/settings/general" replace /> },
      {
        path: 'settings/:tab',
        element: <RequireCapability anyOf={['settings.general:update', 'settings.auth:update']}><Settings /></RequireCapability>,
      },

      { path: '*', element: <NotFound /> },
    ],
  },
])
