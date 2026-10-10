// Central URL builders so links stay consistent across pages.

const seg = (s: string) => encodeURIComponent(s)

export const paths = {
  dashboard: () => '/',
  orders: () => '/orders',
  newOrder: (menu?: { namespace: string; name: string }) =>
    menu ? `/orders/new?menu=${seg(`${menu.namespace}/${menu.name}`)}` : '/orders/new',
  order: (ns: string, name: string, tab?: 'preparations' | 'servings' | 'files' | 'edit') =>
    `/orders/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  preparations: () => '/preparations',
  preparation: (ns: string, name: string, tab?: 'files' | 'changes' | 'approvals') =>
    `/preparations/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  servings: () => '/servings',
  serving: (ns: string, name: string) => `/servings/${seg(ns)}/${seg(name)}`,
  menus: () => '/menus',
  newMenu: () => '/menus/new',
  menu: (ns: string, name: string, tab?: 'orders' | 'edit') =>
    `/menus/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  pantries: () => '/pantries',
  newPantry: () => '/pantries/new',
  pantry: (ns: string, name: string, tab?: 'tags' | 'edit') =>
    `/pantries/${seg(ns)}/${seg(name)}${tab ? `/${tab}` : ''}`,
  settings: (tab: 'general' | 'authentication' = 'general') => `/settings/${tab}`,
}
