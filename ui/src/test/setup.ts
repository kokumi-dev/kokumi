import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'
import { removeImportedListeners } from './helpers'

afterEach(() => {
  cleanup()
  removeImportedListeners()
  vi.useRealTimers()
  localStorage.clear()
  sessionStorage.clear()
  Reflect.deleteProperty(navigator, 'locks')
  history.replaceState(null, '', '/')
})
