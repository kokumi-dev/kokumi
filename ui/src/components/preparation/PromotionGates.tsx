import type { Preparation } from '../../api/types'
import RelativeTime from '../shared/RelativeTime'
import VerdictLine from '../vulnerability/VerdictLine'
import type { Verdict } from '../../utils/vulnerabilities'
import { withinAMinute, type UpstreamVerdict } from '../../utils/promotion'
import styles from '../vulnerability/Vulnerability.module.css'

interface Props {
  preparation: Preparation
  /** The verdict of its upstream gate; null when it names no upstream stages. */
  upstream: UpstreamVerdict | null
}

/**
 * PromotionGates tells, before a promotion, what holds the Preparation: one
 * line per gate the Serving checks before the vulnerability check, which
 * VulnerabilityGate tells. A Preparation they hold can be promoted now: it
 * is queued, and the Serving deploys it once they pass.
 */
export default function PromotionGates({ preparation: prep, upstream }: Props) {
  const signed = prep.conditions?.find((c) => c.type === 'Signed')
  const awaitingApproval = !!prep.approval && !prep.approval.approved
  const awaitingUpstream = !!upstream && !upstream.passes

  const lines: { label: string; verdict: Verdict }[] = []
  if (prep.approval) {
    lines.push({
      label: 'Approvals',
      verdict: prep.approval.approved
        ? { tone: 'success', text: 'Approved', passes: true }
        : { tone: 'warning', text: prep.approval.message ?? prep.approval.state, passes: false },
    })
  }
  lines.push({
    label: 'Signature',
    verdict:
      signed?.status === 'True'
        ? { tone: 'success', text: 'Signed; verified in the registry before it deploys', passes: true }
        : { tone: 'warning', text: signed?.message ?? 'Not signed yet', passes: false },
  })
  if (upstream) lines.push({ label: 'Upstream stages', verdict: upstream })

  const waits = [
    awaitingUpstream && 'its upstream stages deployed its revision',
    awaitingApproval && 'it is approved',
  ].filter(Boolean)
  const soakEnds = awaitingUpstream ? upstream.stages.find((s) => s.soakEndsAt)?.soakEndsAt : undefined

  return (
    <p className={styles.gate}>
      {lines.map((l) => (
        <span key={l.label}>
          {l.label}: <VerdictLine verdict={l.verdict} />
        </span>
      ))}
      {waits.length > 0 && (
        <span className={styles.gateHint}>
          Promoting queues it; the Serving deploys it once {waits.join(' and ')}
          {soakEnds && withinAMinute(soakEnds) ? (
            ', within a minute.'
          ) : soakEnds ? (
            <>
              , at the earliest <RelativeTime iso={soakEnds} />
            </>
          ) : (
            '.'
          )}
        </span>
      )}
    </p>
  )
}
