import { useCallback, useEffect, useRef } from 'react'
import { useBlocker } from 'react-router'
import ConfirmDialog from '../components/shared/ConfirmDialog'

/**
 * Warns before leaving a page with unsaved changes, both for in-app navigation
 * and for reloads/tab closes. Call allowNavigation() right before navigating
 * away after a successful save.
 */
export function useUnsavedChangesGuard(dirty: boolean) {
  const allowRef = useRef(false)

  const blocker = useBlocker(
    useCallback(
      ({ currentLocation, nextLocation }) =>
        dirty && !allowRef.current && currentLocation.pathname !== nextLocation.pathname,
      [dirty],
    ),
  )

  useEffect(() => {
    if (!dirty) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [dirty])

  const allowNavigation = useCallback(() => {
    allowRef.current = true
  }, [])

  const dialog = blocker.state === 'blocked' ? (
    <ConfirmDialog
      title="Discard unsaved changes?"
      confirmLabel="Discard changes"
      variant="danger"
      onCancel={() => blocker.reset()}
      onConfirm={async () => blocker.proceed()}
    >
      You have changes that have not been saved. Leaving this page discards them.
    </ConfirmDialog>
  ) : null

  return { allowNavigation, dialog }
}
