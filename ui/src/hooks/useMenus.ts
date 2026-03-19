import type { Menu } from '../api/types'
import { useSSEEvent } from './useSSEEvent'

export function useMenus(): Menu[] | null {
  return useSSEEvent<Menu[]>('/api/v1/events', 'menus')
}
