import { useEffect, useState } from 'react'
import { listAttestations } from '../../api/client'
import type { Attestation, Preparation } from '../../api/types'
import { Disclosure } from '../ui'
import Section from '../layout/Section'
import EmptyState from '../layout/EmptyState'
import { formatDate } from '../../utils/format'
import { keyLabel, useSigningKeys } from '../../hooks/useSigningKeys'
import styles from './EvidencePanel.module.css'

const knownPredicates: Record<string, string> = {
  'https://sigstore.dev/cosign/sign/v1': 'Signature',
  'https://slsa.dev/provenance/v1': 'Provenance',
  'https://kokumi.dev/attestations/approval/v1': 'Approvals',
}

/**
 * A short name for an attestation. Only verified attestations get one;
 * anything else shows the predicate type or artifactType it claims.
 */
function typeLabel(a: Attestation): string {
  if (a.verification.verified && a.predicateType && knownPredicates[a.predicateType]) {
    return knownPredicates[a.predicateType]
  }
  return a.predicateType || a.artifactType
}

/**
 * AttestationPanel lists the OCI referrers attached to a Preparation's
 * artifact, read live from the registry, and whether each verifies against
 * the keys Kokumi trusts. Only verified ones come with their statement.
 */
export default function AttestationPanel({ preparation: prep }: { preparation: Preparation }) {
  const keys = useSigningKeys()
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
              {a.verification.verified ? (
                <span className={styles.verified}>Verified: signed by {keyLabel(keys, a.verification.keyID ?? '')}</span>
              ) : (
                <span className={styles.unverified}>
                  Not verified{a.verification.error ? `: ${a.verification.error}` : ''}. Its content is not shown.
                </span>
              )}
              {a.verification.verified && a.payload !== undefined && (
                <Disclosure
                  tone="inline"
                  label="Statement"
                  open={open === a.digest}
                  onOpenChange={(next) => setOpen(next ? a.digest : null)}
                >
                  <pre className={styles.code}>{JSON.stringify(a.payload, null, 2)}</pre>
                </Disclosure>
              )}
            </div>
          ))}
        </div>
      )}
    </Section>
  )
}
