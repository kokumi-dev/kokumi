import { useEffect, useState } from 'react'
import { listAttestations } from '../../api/client'
import type { Attestation, Preparation } from '../../api/types'
import { Disclosure } from '../ui'
import Section from '../layout/Section'
import EmptyState from '../layout/EmptyState'
import { formatDate } from '../../utils/format'
import styles from './EvidencePanel.module.css'

const knownTypes: Record<string, string> = {
  'application/vnd.kokumi.provenance.v1+json': 'Provenance',
  'application/vnd.kokumi.approvals.v1+json': 'Approvals',
}

/**
 * A short name for an attestation. Only attestations Kokumi recorded get one;
 * anything else shows its artifactType, whatever it claims to be.
 */
function typeLabel(a: Attestation): string {
  return (a.recorded && knownTypes[a.artifactType]) || a.artifactType
}

/**
 * AttestationPanel lists the OCI referrers attached to a Preparation's
 * artifact, read live from the registry. Only the ones Kokumi recorded come
 * with their payload.
 */
export default function AttestationPanel({ preparation: prep }: { preparation: Preparation }) {
  const [attestations, setAttestations] = useState<Attestation[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [open, setOpen] = useState<string | null>(null)

  // Reload when the Preparation's state changes (delivered via SSE): that is
  // when attestations are added.
  useEffect(() => {
    let cancelled = false
    listAttestations(prep.namespace, prep.name)
      .then((list) => {
        if (cancelled) return
        setAttestations(list)
        setError(null)
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [prep.namespace, prep.name, prep.state, prep.approval?.sealedAt])

  return (
    <Section title="Attestations" description="Evidence attached to this artifact in the registry">
      {error && <p className={styles.error}>{error}</p>}
      {attestations === null && !error && <p className={styles.note}>Loading…</p>}
      {attestations?.length === 0 && <EmptyState text="No attestations are attached to this artifact yet." />}
      {attestations && attestations.length > 0 && (
        <div className={styles.list}>
          {attestations.map((a) => (
            <div key={a.digest} className={styles.item}>
              <div className={styles.itemHeader}>
                <span className={styles.itemTitle}>{typeLabel(a)}</span>
                <span className={styles.itemMeta}>{formatDate(a.createdAt)}</span>
              </div>
              <span className={styles.mono}>{a.digest}</span>
              {!a.recorded ? (
                <p className={styles.note}>Not recorded by Kokumi; its content is not shown.</p>
              ) : a.payload !== undefined ? (
                <Disclosure
                  tone="inline"
                  label="Statement"
                  open={open === a.digest}
                  onOpenChange={(next) => setOpen(next ? a.digest : null)}
                >
                  <pre className={styles.code}>{JSON.stringify(a.payload, null, 2)}</pre>
                </Disclosure>
              ) : (
                <p className={styles.note}>The payload is too large or not JSON and is not shown.</p>
              )}
            </div>
          ))}
        </div>
      )}
    </Section>
  )
}
