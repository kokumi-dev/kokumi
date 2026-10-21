import type { Condition } from '../api/types'
import type { Tone } from '../components/shared/tone'

/** The state of an object as the server reports it, with the conditions it derives from. */
export interface Readiness {
  state?: string
  conditions?: Condition[]
}

/**
 * The tone of a state, from the status of the Ready condition rather than
 * from its reason: True succeeded, False failed, Unknown is under way. An
 * object that waits for another one, or is being deleted, is under way too.
 * Without a Ready condition nothing is known.
 */
export function stateTone({ state, conditions }: Readiness): Tone {
  if (state === 'Waiting' || state === 'Deleting') return 'warning'
  const ready = conditions?.find((c) => c.type === 'Ready')
  if (ready?.status === 'True') return 'success'
  if (ready?.status === 'False') return 'danger'
  return ready ? 'warning' : 'neutral'
}
