import { useCallback, useEffect, useState } from 'react'
import { listApprovals, submitApproval } from '../../api/client'
import type { Approval, ApprovalDecision, Preparation } from '../../api/types'
import Modal from '../shared/Modal'
import Btn from '../shared/Btn'
import styles from './ApprovalModal.module.css'

interface Props {
  preparation: Preparation
  onClose: () => void
}

const maxComment = 1024

/**
 * ApprovalModal shows the immutable vote history of a Preparation and lets the
 * signed-in user approve or request changes. The approver identity is taken
 * from the session by the server; only the decision and comment are sent.
 */
export default function ApprovalModal({ preparation: prep, onClose }: Props) {
  const summary = prep.approval
  const [approvals, setApprovals] = useState<Approval[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [comment, setComment] = useState('')
  const [submitting, setSubmitting] = useState<ApprovalDecision | null>(null)

  const load = useCallback(() => {
    listApprovals(prep.namespace, prep.name)
      .then(setApprovals)
      .catch((e: Error) => setError(e.message))
  }, [prep.namespace, prep.name])

  // Preparation status changes (delivered via SSE) on every vote and on seal.
  useEffect(load, [load, summary?.submissionCount, summary?.sealedAt, summary?.state])

  async function handleVote(decision: ApprovalDecision) {
    setSubmitting(decision)
    setError(null)
    try {
      await submitApproval(prep.namespace, prep.name, decision, comment.trim())
      setComment('')
      load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSubmitting(null)
    }
  }

  const sealed = !!summary?.sealedAt
  const canVote = !!summary && !sealed && prep.state === 'Ready'

  const footer = canVote ? (
    <>
      <Btn variant="danger" onClick={() => handleVote('Reject')} disabled={submitting !== null}>
        {submitting === 'Reject' ? '…' : 'Request changes'}
      </Btn>
      <Btn variant="promote" onClick={() => handleVote('Approve')} disabled={submitting !== null}>
        {submitting === 'Approve' ? '…' : 'Approve'}
      </Btn>
    </>
  ) : undefined

  return (
    <Modal title={`Approvals · ${prep.name}`} onClose={onClose} footer={footer} wide>
      <div className={styles.body}>
        {summary ? (
          <div className={styles.summary}>
            <span className={`${styles.state} ${stateClass(summary.state)}`}>{summary.state}</span>
            <span className={styles.summaryText}>
              {summary.approvedCount} of {summary.requiredApprovals} required approvals
              {summary.rejectedCount > 0 && ` · ${summary.rejectedCount} requested changes`}
              {summary.ineligibleCount > 0 && ` · ${summary.ineligibleCount} not eligible`}
            </span>
            <span className={styles.summaryText}>
              Eligible groups: {summary.policy.allowedGroups.join(', ')}
            </span>
            {sealed && (
              <span className={styles.sealed}>
                Sealed {new Date(summary.sealedAt!).toLocaleString()} — votes are locked
                {summary.attestation && (
                  <span className={styles.mono}> · {summary.attestation}</span>
                )}
              </span>
            )}
          </div>
        ) : (
          <p className={styles.empty}>This Preparation does not require approval.</p>
        )}

        {error && <p className={styles.error}>{error}</p>}

        <div className={styles.history}>
          <span className={styles.sectionTitle}>History</span>
          {approvals === null && !error && <p className={styles.empty}>Loading…</p>}
          {approvals?.length === 0 && <p className={styles.empty}>No votes yet.</p>}
          {approvals?.map((a) => (
            <div key={a.name} className={`${styles.vote} ${a.counted ? '' : styles.voteMuted}`}>
              <div className={styles.voteHeader}>
                <span className={styles.approver} title={`${a.approver.issuer} · ${a.approver.subject}`}>
                  {a.approver.username || a.approver.email || a.approver.subject}
                </span>
                <span className={`${styles.decision} ${a.decision === 'Approve' ? styles.approve : styles.reject}`}>
                  {a.decision === 'Approve' ? 'Approved' : 'Requested changes'}
                </span>
                <span className={styles.time}>{new Date(a.submittedAt).toLocaleString()}</span>
                {a.reason && a.reason !== 'Counted' && (
                  <span className={styles.reason} title={a.message}>{a.reason}</span>
                )}
              </div>
              {a.comment && <p className={styles.comment}>{a.comment}</p>}
            </div>
          ))}
        </div>

        {canVote && (
          <div className={styles.form}>
            <label className={styles.sectionTitle} htmlFor="approval-comment">
              Comment
            </label>
            <textarea
              id="approval-comment"
              className={styles.textarea}
              rows={3}
              maxLength={maxComment}
              value={comment}
              onChange={(e) => setComment(e.target.value)}
              placeholder="Optional review comment"
              disabled={submitting !== null}
            />
          </div>
        )}
      </div>
    </Modal>
  )
}

function stateClass(state: string): string {
  switch (state) {
    case 'Approved':
    case 'ApprovalNotRequired':
      return styles.stateApproved
    case 'ChangesRequested':
      return styles.stateRejected
    default:
      return styles.statePending
  }
}
