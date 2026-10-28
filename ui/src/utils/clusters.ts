import type { DeploymentClusters, DeploymentTarget } from '../api/types'
import type { Tone } from '../components/shared/tone'

/**
 * The tone of a cluster a Serving deploys to: Healthy runs the deployed
 * Preparation, Progressing is deploying it, Pending waits for the first
 * promotion, Unknown is not reported yet, and any other reason, such as
 * Degraded, ClusterNotReady, ClusterConflict or Rejected, holds or failed
 * the deployment there.
 */
export function targetTone(target: Pick<DeploymentTarget, 'state' | 'ready'>): Tone {
  if (target.ready) return 'success'
  switch (target.state) {
    case 'Progressing':
      return 'warning'
    case 'Unknown':
    case 'Pending':
      return 'neutral'
    default:
      return 'danger'
  }
}

/**
 * How the clusters of a Serving read, e.g. "2 of 3 healthy" or, of one,
 * "healthy" or "1 held", from the counts its status keeps; undefined while
 * it deploys nowhere.
 */
export function targetsSummary(counts?: DeploymentClusters): string | undefined {
  if (!counts?.selected) return undefined
  if (counts.selected === 1) return counts.healthy ? 'healthy' : unhealthyCounts(counts) || 'not healthy yet'
  return `${counts.healthy} of ${counts.selected} healthy`
}

/** The counts of a Serving's clusters that are not healthy, in words, e.g. "1 progressing, 2 held". */
export function unhealthyCounts(counts: DeploymentClusters): string {
  return [
    counts.progressing && `${counts.progressing} progressing`,
    counts.degraded && `${counts.degraded} degraded`,
    counts.held && `${counts.held} held`,
  ].filter(Boolean).join(', ')
}

/** The tooltip of a Serving's clusters: those that are not healthy, in words; undefined when every one is. */
export function targetsTitle(counts?: DeploymentClusters): string | undefined {
  return (counts && unhealthyCounts(counts)) || undefined
}
