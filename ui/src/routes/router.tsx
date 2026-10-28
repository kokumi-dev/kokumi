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
import ProductArtifact from '../pages/products/ProductArtifact'
import ProductSecurity from '../pages/products/ProductSecurity'
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
import PreparationImages from '../pages/preparations/PreparationImages'
import PreparationSecurity from '../pages/preparations/PreparationSecurity'
import PreparationArtifact from '../pages/preparations/PreparationArtifact'
import ServingsList from '../pages/servings/ServingsList'
import ServingLayout from '../pages/servings/ServingLayout'
import ServingOverview from '../pages/servings/ServingOverview'
import ServingHistory from '../pages/servings/ServingHistory'
import RepositoriesList from '../pages/repositories/RepositoriesList'
import RepositoryLayout from '../pages/repositories/RepositoryLayout'
import RepositoryOverview from '../pages/repositories/RepositoryOverview'
import RepositoryTags from '../pages/repositories/RepositoryTags'
import RepositoryEditor from '../pages/repositories/RepositoryEditor'
import RegistriesList from '../pages/registries/RegistriesList'
import RegistryDetail from '../pages/registries/RegistryDetail'
import RegistryEditor from '../pages/registries/RegistryEditor'
import EnvironmentsList from '../pages/environments/EnvironmentsList'
import EnvironmentLayout from '../pages/environments/EnvironmentLayout'
import EnvironmentOverview from '../pages/environments/EnvironmentOverview'
import EnvironmentClusters from '../pages/environments/EnvironmentClusters'
import EnvironmentOrders from '../pages/environments/EnvironmentOrders'
import EnvironmentEditor from '../pages/environments/EnvironmentEditor'
import ClustersList from '../pages/clusters/ClustersList'
import ClusterLayout from '../pages/clusters/ClusterLayout'
import ClusterOverview from '../pages/clusters/ClusterOverview'
import ClusterDeployments from '../pages/clusters/ClusterDeployments'
import ClusterEditor from '../pages/clusters/ClusterEditor'

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
          { path: 'artifact', element: <ProductArtifact /> },
          { path: 'security', element: <ProductSecurity /> },
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
          { path: 'images', element: <PreparationImages /> },
          { path: 'security', element: <PreparationSecurity /> },
          { path: 'intent', element: <PreparationIntent /> },
          { path: 'artifact', element: <PreparationArtifact /> },
          { path: 'approvals', element: <PreparationApprovals /> },
        ],
      },

      { path: 'servings', element: <ServingsList /> },
      {
        path: 'servings/:namespace/:name',
        element: <ServingLayout />,
        children: [
          { index: true, element: <ServingOverview /> },
          { path: 'history', element: <ServingHistory /> },
        ],
      },

      { path: 'repositories', element: <RepositoriesList /> },
      { path: 'repositories/new', element: <RequireCapability anyOf={['repositories:create']}><RepositoryEditor /></RequireCapability> },
      { path: 'repositories/:namespace/:name/edit', element: <RequireCapability anyOf={['repositories:update']}><RepositoryEditor /></RequireCapability> },
      {
        path: 'repositories/:namespace/:name',
        element: <RepositoryLayout />,
        children: [
          { index: true, element: <RepositoryOverview /> },
          { path: 'tags', element: <RepositoryTags /> },
        ],
      },

      { path: 'registries', element: <RegistriesList /> },
      { path: 'registries/new', element: <RequireCapability anyOf={['registries:create']}><RegistryEditor /></RequireCapability> },
      { path: 'registries/:namespace/:name/edit', element: <RequireCapability anyOf={['registries:update']}><RegistryEditor /></RequireCapability> },
      { path: 'registries/:namespace/:name', element: <RegistryDetail /> },

      { path: 'environments', element: <EnvironmentsList /> },
      { path: 'environments/new', element: <RequireCapability anyOf={['environments:create']}><EnvironmentEditor /></RequireCapability> },
      { path: 'environments/:namespace/:name/edit', element: <RequireCapability anyOf={['environments:update']}><EnvironmentEditor /></RequireCapability> },
      {
        path: 'environments/:namespace/:name',
        element: <EnvironmentLayout />,
        children: [
          { index: true, element: <EnvironmentOverview /> },
          { path: 'clusters', element: <EnvironmentClusters /> },
          { path: 'orders', element: <EnvironmentOrders /> },
        ],
      },

      { path: 'clusters', element: <ClustersList /> },
      { path: 'clusters/new', element: <RequireCapability anyOf={['clusters:create']}><ClusterEditor /></RequireCapability> },
      { path: 'clusters/:namespace/:name/edit', element: <RequireCapability anyOf={['clusters:update']}><ClusterEditor /></RequireCapability> },
      {
        path: 'clusters/:namespace/:name',
        element: <ClusterLayout />,
        children: [
          { index: true, element: <ClusterOverview /> },
          { path: 'deployments', element: <ClusterDeployments /> },
        ],
      },

      { path: 'settings', element: <Navigate to="/settings/general" replace /> },
      {
        path: 'settings/:tab',
        element: <RequireCapability anyOf={['settings.general:update', 'settings.auth:update', 'settings.signing:update', 'settings.scanning:update']}><Settings /></RequireCapability>,
      },

      { path: '*', element: <NotFound /> },
    ],
  },
])
