// Central URL builders so links stay consistent across pages.

const seg = (s: string) => encodeURIComponent(s)

export const paths = {
  dashboard: () => '/',
  products: () => '/products',
  newProduct: () => '/products/new',
  product: (ns: string, name: string, tab?: 'orders' | 'preview' | 'edit') =>
    `/products/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  orders: () => '/orders',
  /** Optionally preselects the Product the new Order is a variant of. */
  newOrder: (product?: { namespace: string; name: string }) =>
    product ? `/orders/new?product=${seg(`${product.namespace}/${product.name}`)}` : '/orders/new',
  order: (ns: string, name: string, tab?: 'preparations' | 'servings' | 'files' | 'edit') =>
    `/orders/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  preparations: () => '/preparations',
  preparation: (ns: string, name: string, tab?: 'files' | 'changes' | 'intent' | 'attestations' | 'approvals') =>
    `/preparations/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  servings: () => '/servings',
  serving: (ns: string, name: string) => `/servings/${seg(ns)}/${seg(name)}`,
  pantries: () => '/pantries',
  newPantry: () => '/pantries/new',
  pantry: (ns: string, name: string, tab?: 'tags' | 'edit') =>
    `/pantries/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  settings: (tab: 'general' | 'authentication' = 'general') => `/settings/${tab}`,
}
