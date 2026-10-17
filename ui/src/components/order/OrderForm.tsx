import { useState, useEffect } from 'react'
import { Link } from 'react-router'
import { load, dump } from 'js-yaml'
import YamlEditor from '../shared/YamlEditor'
import { Disclosure, TabButtons } from '../ui'
import CommitBox from '../shared/CommitBox'
import PreviewTab from '../shared/PreviewTab'
import DestinationEditor from '../shared/DestinationEditor'
import HelmValuesEditor from '../shared/HelmValuesEditor'
import PatchesEditor from '../shared/PatchesEditor'
import DiffTab from './DiffTab'
import type { Order, OrderFormData, OrderPreviewRequest, OverridePolicy, Product } from '../../api/types'
import { emptyOrderForm, orderToFormData } from '../../api/types'
import { previewOrder, previewOrderFiles } from '../../api/client'
import { useChartInfo } from '../../hooks/useChartInfo'
import { assertKnownKeys, patchesFromYaml, patchesToYaml } from '../../utils/yaml'
import {
  allowedTargetLabel,
  allowsPatches,
  allowsValues,
  describePatchesPolicy,
  describeValuesPolicy,
  patchesPolicy,
  productSourceLabel,
  valuesPolicy,
} from '../../utils/products'
import { paths } from '../../routes/paths'
import styles from '../shared/Form.module.css'
import layout from '../layout/layout.module.css'

interface Props {
  /** When provided the form is in "edit" mode. */
  order?: Order
  /** Product preselected for a new Order. */
  initialProduct?: { namespace: string; name: string }
  /** All Products; the picker offers the ones in the Order's namespace. */
  products: Product[]
  onCancel: () => void
  onSubmit: (data: OrderFormData, commitMessage: string) => Promise<void>
  /** Reports whether the form holds unsaved changes. */
  onDirtyChange?: (dirty: boolean) => void
}

// ── YAML serialisation helpers ────────────────────────────────────────────────

type Tab = 'form' | 'yaml' | 'preview' | 'diff'

function parseGroups(text: string): string[] {
  return [...new Set(text.split(',').map((g) => g.trim()).filter(Boolean))]
}

function hasValues(values: Record<string, unknown>): boolean {
  return Object.keys(values).length > 0
}

/**
 * The YAML view of the Order spec. Values are left out only when the Product
 * is known not to render with Helm (helm is false, not undefined).
 */
function formToYaml(data: OrderFormData, helm: boolean | undefined): string {
  const doc: Record<string, unknown> = {
    productRef: { name: data.productRef.name },
  }
  if (helm !== false && hasValues(data.values)) doc.values = data.values
  if (data.patches.length > 0) doc.patches = patchesToYaml(data.patches)
  if (data.destination?.pantryRef?.name) {
    doc.destination = { pantryRef: { name: data.destination.pantryRef.name } }
  } else if (data.destination?.oci) {
    doc.destination = { oci: data.destination.oci }
  }
  doc.promotion = data.approvals ? { mode: data.mode, approvals: data.approvals } : { mode: data.mode }
  return dump(doc, { lineWidth: 100 })
}

interface ParseContext {
  /** The Product of an existing Order, which cannot be changed. */
  lockedProduct?: string
  /** Whether the named Product renders with Helm; undefined when it is unknown. */
  rendersWithHelm: (product: string) => boolean | undefined
}

function yamlToPartialForm(text: string, ctx: ParseContext): Omit<OrderFormData, 'name' | 'namespace' | 'edits'> {
  const doc = load(text) as Record<string, unknown>
  if (!doc || typeof doc !== 'object') throw new Error('YAML must be a mapping')
  assertKnownKeys(doc, ['productRef', 'values', 'destination', 'patches', 'promotion'], '')

  const rawProductRef = doc.productRef as Record<string, unknown> | undefined
  assertKnownKeys(rawProductRef, ['name'], 'productRef')
  const product = String(rawProductRef?.name ?? '')
  if (ctx.lockedProduct !== undefined && product !== ctx.lockedProduct) {
    throw new Error(`productRef cannot be changed: this Order is a variant of ${ctx.lockedProduct}`)
  }

  const rawValues = doc.values
  if (rawValues !== undefined && rawValues !== null) {
    if (typeof rawValues !== 'object' || Array.isArray(rawValues)) throw new Error('values must be a mapping')
    if (ctx.rendersWithHelm(product) === false) {
      throw new Error(`values are only valid when the Product renders with Helm, and ${product} does not`)
    }
  }

  // Resolve destination — at most one of oci or pantryRef
  const dst = doc.destination as Record<string, unknown> | undefined
  let destination: OrderFormData['destination'] = {}
  if (dst) {
    const dstPantryRef = dst.pantryRef as Record<string, string> | undefined
    if (dstPantryRef?.name) {
      destination = { pantryRef: { name: dstPantryRef.name } }
    } else {
      destination = { oci: (dst.oci as string) ?? '' }
    }
  }

  const promotion = doc.promotion as Record<string, unknown> | undefined
  const rawApprovals = promotion?.approvals as Record<string, unknown> | undefined
  assertKnownKeys(promotion, ['mode', 'approvals'], 'promotion')
  assertKnownKeys(rawApprovals, ['requiredApprovals', 'allowedGroups'], 'promotion.approvals')
  if (promotion?.mode !== undefined && promotion.mode !== 'Manual' && promotion.mode !== 'Automatic') {
    throw new Error('promotion.mode must be Manual or Automatic')
  }
  if (rawApprovals) {
    const n = rawApprovals.requiredApprovals
    if (typeof n !== 'number' || !Number.isInteger(n) || n < 1 || n > 32) {
      throw new Error('promotion.approvals.requiredApprovals must be an integer between 1 and 32')
    }
  }

  return {
    productRef: { name: product },
    values: (rawValues as Record<string, unknown> | null | undefined) ?? {},
    destination,
    mode: promotion?.mode === 'Automatic' ? 'Automatic' : 'Manual',
    approvals: rawApprovals
      ? {
          requiredApprovals: rawApprovals.requiredApprovals as number,
          allowedGroups: Array.isArray(rawApprovals.allowedGroups)
            ? (rawApprovals.allowedGroups as unknown[]).map(String)
            : [],
        }
      : undefined,
    patches: patchesFromYaml(doc.patches),
  }
}

// ── Main component ────────────────────────────────────────────────────────────

export default function OrderForm({ order, initialProduct, products, onCancel, onSubmit, onDirtyChange }: Props) {
  const isEdit = !!order
  const showDiffTab = isEdit && !!order?.activePreparation
  const [tab, setTab] = useState<Tab>('form')
  const [formData, setFormData] = useState<OrderFormData>(() =>
    order ? orderToFormData(order) : emptyOrderForm(initialProduct),
  )

  function productFor(data: OrderFormData): Product | undefined {
    return products.find((p) => p.namespace === data.namespace && p.name === data.productRef.name)
  }

  /** Whether the Order's Product renders with Helm; undefined while it is unknown. */
  function helmFor(data: OrderFormData): boolean | undefined {
    const product = productFor(data)
    return product ? !!product.render?.helm : undefined
  }

  /** Why no Order can be created for the picked Product; edits of existing Orders are fine. */
  function deletingReason(data: OrderFormData): string | undefined {
    const product = productFor(data)
    return !isEdit && product?.deletionTimestamp
      ? `Product ${product.name} is being deleted; pick another Product`
      : undefined
  }

  const parseContext: ParseContext = {
    lockedProduct: order?.productRef.name,
    rendersWithHelm: (name) => helmFor({ ...formData, productRef: { name } }),
  }

  const [yamlText, setYamlText] = useState(() => formToYaml(formData, helmFor(formData)))
  const [yamlError, setYamlError] = useState<string | null>(null)

  const [initialYaml] = useState(() => formToYaml(formData, helmFor(formData)))
  const [initialIdentity] = useState(() => `${formData.namespace}/${formData.name}`)

  /**
   * The data as sent: values are dropped only for a Product known not to
   * render with Helm, so an unknown Product keeps the Order's values.
   */
  function sendable(data: OrderFormData): OrderFormData {
    return helmFor(data) === false ? { ...data, values: {} } : data
  }

  function previewRequest(data: OrderFormData): OrderPreviewRequest | null {
    if (!data.productRef.name) return null
    const sent = sendable(data)
    return {
      namespace: sent.namespace,
      name: sent.name,
      productRef: sent.productRef,
      values: hasValues(sent.values) ? sent.values : undefined,
      destination: sent.destination,
      patches: sent.patches,
      edits: sent.edits,
    }
  }

  // ── Tab switching ──────────────────────────────────────────────────────────

  function selectTab(next: Tab) {
    if (next === 'yaml') {
      setYamlText(formToYaml(formData, helmFor(formData)))
      setYamlError(null)
      setTab('yaml')
      return
    }
    if (tab === 'yaml') {
      try {
        const partial = yamlToPartialForm(yamlText, parseContext)
        setFormData((prev) => ({ ...prev, ...partial }))
        setYamlError(null)
      } catch (e) {
        setYamlError(e instanceof Error ? e.message : String(e))
        return
      }
    }
    setTab(next)
  }

  // ── Submit ─────────────────────────────────────────────────────────────────

  async function handleCommit(commitMessage: string) {
    let data = formData
    if (tab === 'yaml') {
      try {
        data = { ...formData, ...yamlToPartialForm(yamlText, parseContext) }
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e)
        setYamlError(msg)
        throw new Error(`Fix the YAML before saving: ${msg}`, { cause: e })
      }
    }
    if (!data.productRef.name) throw new Error('Pick the Product this Order is a variant of')
    const deleting = deletingReason(data)
    if (deleting) throw new Error(deleting)
    await onSubmit(sendable(data), commitMessage)
  }

  // ── Render ─────────────────────────────────────────────────────────────────

  let isDirty: boolean
  if (tab === 'yaml') {
    try {
      const data = { ...formData, ...yamlToPartialForm(yamlText, parseContext) }
      isDirty = formToYaml(data, helmFor(data)) !== initialYaml
    } catch {
      isDirty = true
    }
  } else {
    isDirty = formToYaml(formData, helmFor(formData)) !== initialYaml
  }
  // Name/namespace are not part of the YAML view but still count as changes.
  if (`${formData.namespace}/${formData.name}` !== initialIdentity) isDirty = true

  useEffect(() => {
    onDirtyChange?.(isDirty)
  }, [isDirty, onDirtyChange])

  const blocked = deletingReason(formData)

  return (
    <>
      <TabButtons<Tab>
        tabs={[
          { id: 'form', label: 'Form' },
          { id: 'yaml', label: 'YAML' },
          { id: 'preview', label: 'Preview' },
          ...(showDiffTab ? [{ id: 'diff' as const, label: 'Diff' }] : []),
        ]}
        active={tab}
        onSelect={selectTab}
      />

      <div className={`${styles.formCard} ${styles.tabContent}`}>
        {tab === 'form' && (
          <FormView
            formData={formData}
            isEdit={isEdit}
            product={productFor(formData)}
            products={products}
            deletingReason={blocked}
            onFieldChange={(key, val) => setFormData((prev) => ({ ...prev, [key]: val }))}
          />
        )}
        {tab === 'yaml' && (
          <>
            <YamlEditor value={yamlText} onChange={(v) => { setYamlText(v); setYamlError(null) }} />
            {yamlError && <p className={styles.yamlError}>Parse error: {yamlError}</p>}
          </>
        )}
        {tab === 'preview' && (
          <PreviewTab
            request={previewRequest(formData)}
            renderManifest={previewOrder}
            renderFiles={previewOrderFiles}
            placeholder="Pick a Product to preview the rendered manifest."
          />
        )}
        {tab === 'diff' && showDiffTab && (
          <DiffTab request={previewRequest(formData)!} order={order!} />
        )}
      </div>

      <CommitBox
        title={isEdit ? 'Commit changes' : 'Create Order'}
        submitLabel={isEdit ? 'Commit changes' : 'Create Order'}
        onCommit={handleCommit}
        onCancel={onCancel}
        disabled={!!blocked || (isEdit && !isDirty)}
        disabledReason={blocked ?? 'No changes to commit yet'}
      />
    </>
  )
}

// ── FormView ──────────────────────────────────────────────────────────────────

interface FormViewProps {
  formData: OrderFormData
  isEdit: boolean
  /** The selected Product, once it is loaded. */
  product?: Product
  products: Product[]
  /** Set when the selected Product is being deleted and the Order is new. */
  deletingReason?: string
  onFieldChange: <K extends keyof OrderFormData>(key: K, val: OrderFormData[K]) => void
}

function FormView({ formData, isEdit, product, products, deletingReason, onFieldChange }: FormViewProps) {
  const [isDestOpen, setIsDestOpen] = useState(
    !!(formData.destination?.oci || formData.destination?.pantryRef?.name),
  )
  const [isPatchesOpen, setIsPatchesOpen] = useState(formData.patches.length > 0)
  const [groupsText, setGroupsText] = useState(formData.approvals?.allowedGroups.join(', ') ?? '')

  const helm = !!product?.render?.helm
  const chart = useChartInfo(helm ? product?.resolvedSource : undefined, formData.namespace)
  // A new Order would hold up the deletion of its Product.
  const candidates = products.filter((p) => p.namespace === formData.namespace && !p.deletionTimestamp)
  // Values of an unknown Product are kept, so they stay visible; values a
  // Product does not allow stay visible so they can be removed.
  const withValues = hasValues(formData.values)
  const showValues = product ? helm : withValues
  const editValues = !product || allowsValues(product) || withValues

  return (
    <div className={styles.formGrid}>
      {/* Name + Namespace */}
      <div className={styles.row2}>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Name</label>
          <input
            className={`${styles.input} ${isEdit ? styles.inputDisabled : ''}`}
            value={formData.name}
            onChange={(e) => onFieldChange('name', e.target.value)}
            readOnly={isEdit}
            placeholder="my-app-dev"
          />
        </div>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Namespace</label>
          <input
            className={`${styles.input} ${isEdit ? styles.inputDisabled : ''}`}
            value={formData.namespace}
            onChange={(e) => onFieldChange('namespace', e.target.value)}
            readOnly={isEdit}
            placeholder="kokumi"
          />
        </div>
      </div>

      {/* Product — required, and fixed once the Order exists */}
      <div className={styles.fieldGroup}>
        <label className={styles.label}>Product</label>
        {isEdit ? (
          <input
            className={`${styles.input} ${styles.inputDisabled}`}
            value={formData.productRef.name}
            readOnly
          />
        ) : (
          <select
            className={styles.input}
            value={formData.productRef.name}
            onChange={(e) => onFieldChange('productRef', { name: e.target.value })}
          >
            <option value="">— select a Product —</option>
            {formData.productRef.name && (!product || deletingReason) && (
              <option value={formData.productRef.name}>
                {formData.productRef.name} ({product ? 'being deleted' : 'not found'})
              </option>
            )}
            {candidates.map((p) => (
              <option key={p.name} value={p.name}>{p.name}</option>
            ))}
          </select>
        )}
        {deletingReason ? (
          <span className={styles.fieldError}>{deletingReason}.</span>
        ) : product ? (
          <span className={styles.hint}>
            <span className={layout.mono}>{productSourceLabel(product)}</span>
            {' · '}{helm ? 'Helm' : 'Manifests'}{' · '}
            <Link className={layout.inlineLink} to={paths.product(product.namespace, product.name)}>Open Product</Link>
          </span>
        ) : !isEdit && candidates.length === 0 ? (
          <span className={styles.hint}>
            No Products in namespace {formData.namespace || '—'} yet.{' '}
            <Link className={layout.inlineLink} to={paths.newProduct()}>Create a Product</Link>
          </span>
        ) : isEdit ? (
          <span className={styles.hint}>This Product was not found; the Order cannot render until it exists.</span>
        ) : (
          <span className={styles.hint}>The Order is a variant of this Product and only holds what differs.</span>
        )}
      </div>

      {/* Values — only for Products that render with Helm, or kept for an unknown one */}
      {showValues && (
        <div>
          <p className={styles.sectionTitle}>Values</p>
          <div className={editValues ? styles.helmCard : undefined}>
            {product ? (
              <PolicyBanner
                policy={valuesPolicy(product)}
                description={describeValuesPolicy(valuesPolicy(product))}
                listed={product.overrides?.values?.paths ?? []}
                clearHint={withValues ? 'remove these values to save the Order' : undefined}
              />
            ) : (
              <span className={styles.hint}>The Product was not found; the Order keeps these values as they are.</span>
            )}
            {editValues && (
              <HelmValuesEditor
                // A different Product brings different chart defaults.
                key={product ? `${product.namespace}/${product.name}` : formData.productRef.name}
                values={formData.values}
                onChange={(v) => onFieldChange('values', v)}
                chartInfo={chart.info}
                chartInfoLoading={chart.loading}
              />
            )}
          </div>
        </div>
      )}

      {/* Destination (collapsible) */}
      <Disclosure
        label="Destination"
        summary={formData.destination?.oci || (formData.destination?.pantryRef?.name && `pantry: ${formData.destination.pantryRef.name}`)}
        open={isDestOpen}
        onOpenChange={setIsDestOpen}
      >
        <div className={styles.formGrid} style={{ gap: 10, marginTop: 4 }}>
          <DestinationEditor
            destination={formData.destination ?? {}}
            onChange={(dest) => onFieldChange('destination', dest)}
            namespace={formData.namespace}
            name={formData.name}
          />
        </div>
      </Disclosure>

      {/* Promotion */}
      <label className={styles.checkRow}>
        <input
          type="checkbox"
          checked={formData.mode === 'Automatic'}
          onChange={(e) => onFieldChange('mode', e.target.checked ? 'Automatic' : 'Manual')}
        />
        Automatic promotion — promote new Preparations as soon as they are ready and approved
      </label>

      {/* Approval gate */}
      <label className={styles.checkRow}>
        <input
          type="checkbox"
          checked={!!formData.approvals}
          onChange={(e) => {
            onFieldChange('approvals', e.target.checked ? { requiredApprovals: 1, allowedGroups: [] } : undefined)
            setGroupsText('')
          }}
        />
        Require approvals — block promotion until enough eligible reviewers approve
      </label>
      {formData.approvals && (
        <div className={styles.row2}>
          <div className={styles.fieldGroup}>
            <label className={styles.label}>Required approvals</label>
            <input
              className={styles.input}
              type="number"
              min={1}
              max={32}
              value={formData.approvals.requiredApprovals}
              onChange={(e) =>
                onFieldChange('approvals', {
                  ...formData.approvals!,
                  requiredApprovals: Math.max(1, Math.min(32, Number(e.target.value) || 1)),
                })
              }
            />
          </div>
          <div className={styles.fieldGroup}>
            <label className={styles.label}>Allowed groups</label>
            <input
              className={styles.input}
              value={groupsText}
              onChange={(e) => {
                setGroupsText(e.target.value)
                onFieldChange('approvals', {
                  ...formData.approvals!,
                  allowedGroups: parseGroups(e.target.value),
                })
              }}
              placeholder="release-approvers, security"
            />
          </div>
        </div>
      )}

      {/* Patches (collapsible) */}
      <Disclosure
        label="Patches"
        summary={formData.patches.length > 0 && `${formData.patches.length} patch${formData.patches.length === 1 ? '' : 'es'}`}
        open={isPatchesOpen}
        onOpenChange={setIsPatchesOpen}
      >
        <div className={styles.formGrid} style={{ gap: 10, marginTop: 4 }}>
          {product && (
            <PolicyBanner
              policy={patchesPolicy(product)}
              description={describePatchesPolicy(patchesPolicy(product))}
              listed={(product.overrides?.patches?.targets ?? []).map(allowedTargetLabel)}
              clearHint={formData.patches.length > 0 ? 'remove these patches to save the Order' : undefined}
            />
          )}
          {(!product || allowsPatches(product) || formData.patches.length > 0) && (
            <div>
              <PatchesEditor patches={formData.patches} onChange={(p) => onFieldChange('patches', p)} />
            </div>
          )}
        </div>
      </Disclosure>
    </div>
  )
}

// ── PolicyBanner ──────────────────────────────────────────────────────────────

interface PolicyBannerProps {
  policy: OverridePolicy
  /** What the policy lets Orders do. */
  description: string
  /** What a Listed policy allows. */
  listed: string[]
  /** Asks to clear what a None policy rejects; shown when given. */
  clearHint?: string
}

/** What the Product lets this Order change in one category. */
function PolicyBanner({ policy, description, listed, clearHint }: PolicyBannerProps) {
  if (policy === 'All') {
    return (
      <div className={styles.policyBannerOpen}>
        <span className={styles.policyIcon}>✓</span>
        {description}
      </div>
    )
  }
  if (policy === 'Listed') {
    return (
      <div className={styles.policyBanner}>
        <span className={styles.policyIcon}>📋</span>
        {description}:{' '}
        {listed.map((item) => (
          <span key={item} className={styles.policyChip}>{item}</span>
        ))}
      </div>
    )
  }
  return (
    <div className={styles.policyBanner}>
      <span className={styles.policyIcon}>🔒</span>
      {description}{clearHint && `; ${clearHint}`}
    </div>
  )
}
