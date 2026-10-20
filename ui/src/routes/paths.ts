/** The tabs of the Settings page. */
export const settingsTabs = ['general', 'authentication', 'signing', 'scanning'] as const
export type SettingsTab = typeof settingsTabs[number]

// Central URL builders so links stay consistent across pages.

const seg = (s: string) => encodeURIComponent(s)

export const paths = {
  dashboard: () => '/',
  products: () => '/products',
  /** Optionally pre-fills the source and Registry from a Repository, and the version. */
  newProduct: (repository?: { namespace: string; name: string }, version?: string) =>
    repository
      ? `/products/new?repository=${seg(`${repository.namespace}/${repository.name}`)}${version ? `&version=${seg(version)}` : ''}`
      : '/products/new',
  product: (ns: string, name: string, tab?: 'orders' | 'preview' | 'artifact' | 'security' | 'edit') =>
    `/products/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  orders: () => '/orders',
  /** Optionally preselects the Product the new Order is a variant of. */
  newOrder: (product?: { namespace: string; name: string }) =>
    product ? `/orders/new?product=${seg(`${product.namespace}/${product.name}`)}` : '/orders/new',
  order: (ns: string, name: string, tab?: 'preparations' | 'servings' | 'files' | 'edit') =>
    `/orders/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  preparations: () => '/preparations',
  preparation: (ns: string, name: string, tab?: 'files' | 'changes' | 'images' | 'security' | 'intent' | 'artifact' | 'approvals') =>
    `/preparations/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  servings: () => '/servings',
  serving: (ns: string, name: string, tab?: 'history') => `/servings/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  repositories: () => '/repositories',
  newRepository: () => '/repositories/new',
  repository: (ns: string, name: string, tab?: 'tags' | 'edit') =>
    `/repositories/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  registries: () => '/registries',
  newRegistry: () => '/registries/new',
  registry: (ns: string, name: string, tab?: 'edit') =>
    `/registries/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  settings: (tab: SettingsTab = 'general') => `/settings/${tab}`,
}
