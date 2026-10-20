import { useState } from 'react'
import type { UpstreamStage } from '../../api/types'
import { useOrders } from '../../hooks/useOrders'
import { Button } from '../ui'
import styles from '../shared/Form.module.css'

interface UpstreamStagesEditorProps {
  namespace: string
  /** The Product of the Order; only its other variants can be upstream stages. */
  product: string
  /** The name of the Order, which cannot run after itself. */
  self: string
  value: UpstreamStage[]
  onChange: (stages: UpstreamStage[]) => void
}

/** A soak in minutes as the editor shows it; empty for none. */
function soakText(seconds?: number): string {
  return seconds ? String(Math.round((seconds / 60) * 100) / 100) : ''
}

/** The soak typed in minutes, in whole seconds; 0 when it is empty or not a positive number. */
function soakSeconds(text: string): number {
  const minutes = Number(text)
  return text.trim() && Number.isFinite(minutes) && minutes > 0 ? Math.round(minutes * 60) : 0
}

/**
 * UpstreamStagesEditor picks the Orders an Order runs after: the other
 * variants of its Product in its namespace. The Order deploys a Product
 * revision once each of them ran its own Preparation of that revision
 * healthily for its soak time.
 */
export default function UpstreamStagesEditor({ namespace, product, self, value, onChange }: UpstreamStagesEditorProps) {
  const orders = useOrders()
  const [texts, setTexts] = useState<Record<string, string>>(() =>
    Object.fromEntries(value.map((s) => [s.order, soakText(s.soakSeconds)])),
  )
  const candidates = (orders ?? []).filter((o) => o.namespace === namespace && o.productRef.name === product && o.name !== self)
  const missing = value.filter((s) => !candidates.some((c) => c.name === s.order))
  const selected = new Map(value.map((s) => [s.order, s]))

  function toggle(order: string, on: boolean) {
    onChange(on ? [...value, { order }] : value.filter((s) => s.order !== order))
    setTexts((prev) => ({ ...prev, [order]: '' }))
  }

  function setSoak(order: string, text: string) {
    setTexts((prev) => ({ ...prev, [order]: text }))
    const seconds = soakSeconds(text)
    onChange(value.map((s) => (s.order === order ? (seconds ? { order, soakSeconds: seconds } : { order }) : s)))
  }

  if (!product) return null

  return (
    <div className={styles.fieldGroup}>
      <span className={styles.label}>Runs after</span>
      {orders === null ? (
        <span className={styles.hint}>Loading Orders…</span>
      ) : candidates.length === 0 && missing.length === 0 ? (
        <span className={styles.hint}>
          No other Orders of Product {product} in <code>{namespace}</code>. This Order deploys without upstream stages.
        </span>
      ) : (
        <>
          {candidates.map((o) => {
            const stage = selected.get(o.name)
            return (
              <div key={o.name} className={styles.row2}>
                <label className={styles.checkRow}>
                  <input type="checkbox" checked={!!stage} onChange={(e) => toggle(o.name, e.target.checked)} />
                  <span>{o.name}</span>
                  <span className={styles.hint}>{o.mode}</span>
                </label>
                {stage && (
                  <input
                    className={styles.input}
                    type="number"
                    min={0}
                    step="any"
                    aria-label={`Soak after ${o.name} (minutes)`}
                    value={texts[o.name] ?? soakText(stage.soakSeconds)}
                    onChange={(e) => setSoak(o.name, e.target.value)}
                    placeholder="Soak (minutes), none by default"
                  />
                )}
              </div>
            )
          })}
          {missing.map((s) => (
            <div key={s.order} className={styles.checkRow}>
              <span className={styles.fieldError}>Order {s.order} does not exist; the Order waits for it</span>
              <Button variant="ghost" size="sm" onClick={() => toggle(s.order, false)}>Remove</Button>
            </div>
          ))}
          <span className={styles.hint}>
            Each Product revision deploys here once every selected Order ran its own Preparation of it healthily for its
            soak time. Nothing is copied between the Orders: each renders and deploys its own Preparation.
          </span>
        </>
      )}
    </div>
  )
}
