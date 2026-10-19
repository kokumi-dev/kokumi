import { useState, useEffect } from 'react'
import { Link } from 'react-router'
import { load, dump } from 'js-yaml'
import YamlEditor from '../shared/YamlEditor'
import { Disclosure, TabButtons } from '../ui'
import CommitBox from '../shared/CommitBox'
import PreviewTab from '../shared/PreviewTab'
import DestinationEditor from '../shared/DestinationEditor'
import RegistryRefsEditor from '../shared/RegistryRefsEditor'
import HelmValuesEditor from '../shared/HelmValuesEditor'
import PatchesEditor from '../shared/PatchesEditor'
import DiffTab from './DiffTab'
import type { Order, OrderFormData, OrderPreviewRequest, OverridePolicy, Product, Severity, VulnerabilityCheck } from '../../api/types'
import { emptyOrderForm, imagePolicy, orderToFormData, severities } from '../../api/types'
import { previewOrder } from '../../api/client'
import { useChartInfo } from '../../hooks/useChartInfo'
import { assertKnownKeys, patchesFromYaml, patchesToYaml, registryRefsFromYaml } from '../../utils/yaml'
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

/** The lowest maxAgeSeconds of a vulnerability check: an hour. */
const minScanAgeSeconds = 3600

/** A vulnerability check without its unset fields, as written in YAML and compared for changes. */
function normalizedCheck(c: VulnerabilityCheck): VulnerabilityCheck {
  return {
    maxSeverity: c.maxSeverity,
    ...(c.ignoreUnfixed ? { ignoreUnfixed: true } : {}),
    ...(c.maxAgeSeconds !== undefined ? { maxAgeSeconds: c.maxAgeSeconds } : {}),
  }
}

function vulnerabilityCheckFromYaml(raw: Record<string, unknown>): VulnerabilityCheck {
  const path = 'promotion.checks.vulnerabilities'
  if (!severities.includes(raw.maxSeverity as Severity)) {
    throw new Error(`${path}.maxSeverity must be one of ${severities.join(', ')}`)
  }
  if (raw.ignoreUnfixed !== undefined && typeof raw.ignoreUnfixed !== 'boolean') {
    throw new Error(`${path}.ignoreUnfixed must be true or false`)
  }
  const age = raw.maxAgeSeconds
  if (age !== undefined && (typeof age !== 'number' || !Number.isInteger(age) || age < minScanAgeSeconds)) {
    throw new Error(`${path}.maxAgeSeconds must be an integer of at least ${minScanAgeSeconds}`)
  }
  return normalizedCheck({
    maxSeverity: raw.maxSeverity as Severity,
    ignoreUnfixed: raw.ignoreUnfixed as boolean | undefined,
    maxAgeSeconds: age as number | undefined,
  })
}

/** A max scan age in hours as the form shows it; empty for the default. */
function scanAgeText(seconds?: number): string {
  return seconds === undefined ? '' : String(Math.round((seconds / 3600) * 100) / 100)
}

/** The max scan age typed in hours, in seconds: undefined when empty, 0 when it is not a positive number. */
function scanAgeSeconds(text: string): number | undefined {
  if (!text.trim()) return undefined
  const hours = Number(text)
  return Number.isFinite(hours) && hours > 0 ? Math.round(hours * 3600) : 0
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
  if (data.destination?.url) doc.destination = { url: data.destination.url }
  if (data.registryRefs.length > 0) doc.registryRefs = data.registryRefs.map((r) => ({ name: r.name }))
  const promotion: Record<string, unknown> = { mode: data.mode }
  if (data.approvals) promotion.approvals = data.approvals
  if (data.checks?.vulnerabilities) promotion.checks = { vulnerabilities: normalizedCheck(data.checks.vulnerabilities) }
  doc.promotion = promotion
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
  assertKnownKeys(doc, ['productRef', 'values', 'destination', 'registryRefs', 'patches', 'promotion'], '')

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

  const dst = doc.destination as Record<string, unknown> | undefined
  assertKnownKeys(dst, ['url'], 'destination')
  const destination: OrderFormData['destination'] = dst?.url ? { url: String(dst.url) } : {}

  const promotion = doc.promotion as Record<string, unknown> | undefined
  const rawApprovals = promotion?.approvals as Record<string, unknown> | undefined
  const rawChecks = promotion?.checks as Record<string, unknown> | undefined
  const rawVulnerabilities = rawChecks?.vulnerabilities as Record<string, unknown> | undefined
  assertKnownKeys(promotion, ['mode', 'approvals', 'checks'], 'promotion')
  assertKnownKeys(rawApprovals, ['requiredApprovals', 'allowedGroups'], 'promotion.approvals')
  assertKnownKeys(rawChecks, ['vulnerabilities'], 'promotion.checks')
  assertKnownKeys(rawVulnerabilities, ['maxSeverity', 'ignoreUnfixed', 'maxAgeSeconds'], 'promotion.checks.vulnerabilities')
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
    registryRefs: registryRefsFromYaml(doc.registryRefs),
    mode: promotion?.mode === 'Automatic' ? 'Automatic' : 'Manual',
    approvals: rawApprovals
      ? {
          requiredApprovals: rawApprovals.requiredApprovals as number,
          allowedGroups: Array.isArray(rawApprovals.allowedGroups)
            ? (rawApprovals.allowedGroups as unknown[]).map(String)
            : [],
        }
      : undefined,
    checks: rawVulnerabilities ? { vulnerabilities: vulnerabilityCheckFromYaml(rawVulnerabilities) } : undefined,
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
    const scanAge = data.checks?.vulnerabilities?.maxAgeSeconds
    if (scanAge !== undefined && scanAge < minScanAgeSeconds) throw new Error('The max scan age must be at least 1 hour')
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
            render={previewOrder}
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
  const [isDestOpen, setIsDestOpen] = useState(!!formData.destination?.url || formData.registryRefs.length > 0)
  const [isPatchesOpen, setIsPatchesOpen] = useState(formData.patches.length > 0)
  const [groupsText, setGroupsText] = useState(formData.approvals?.allowedGroups.join(', ') ?? '')
  const [maxAgeText, setMaxAgeText] = useState(scanAgeText(formData.checks?.vulnerabilities?.maxAgeSeconds))
  const check = formData.checks?.vulnerabilities
  const maxAgeInvalid = check?.maxAgeSeconds !== undefined && check.maxAgeSeconds < minScanAgeSeconds
  const productPolicy = product && imagePolicy(product.images?.policy)

  function setCheck(next?: VulnerabilityCheck) {
    onFieldChange('checks', next ? { vulnerabilities: normalizedCheck(next) } : undefined)
  }

  const helm = !!product?.render?.helm
  const chart = useChartInfo(helm ? product?.resolvedSource : undefined, formData.namespace, product?.registryRefs)
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
        summary={formData.destination?.url}
        open={isDestOpen}
        onOpenChange={setIsDestOpen}
      >
        <div className={styles.formGrid} style={{ gap: 10, marginTop: 4 }}>
          <DestinationEditor
            destination={formData.destination ?? {}}
            onChange={(dest) => onFieldChange('destination', dest)}
            namespace={formData.namespace}
            name={formData.name}
            registryRefs={formData.registryRefs}
            defaultHint="Kokumi pushes the Preparations to the in-cluster registry"
          />
          <RegistryRefsEditor
            namespace={formData.namespace}
            value={formData.registryRefs}
            onChange={(refs) => onFieldChange('registryRefs', refs)}
            owns="the destination"
          />
          <span className={styles.hint}>
            The source and the images are pulled through the Product's Registries; Argo CD needs its own credentials to
            pull the destination.
          </span>
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

      {/* Vulnerability check */}
      <label className={styles.checkRow}>
        <input
          type="checkbox"
          checked={!!check}
          onChange={(e) => {
            setCheck(e.target.checked ? { maxSeverity: 'High' } : undefined)
            setMaxAgeText('')
          }}
        />
        Vulnerability check — block promotion while the images have findings at or above a severity
      </label>
      {check && (
        <>
          <div className={styles.row2}>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="check-max-severity">Max severity</label>
              <select
                id="check-max-severity"
                className={styles.input}
                value={check.maxSeverity}
                onChange={(e) => setCheck({ ...check, maxSeverity: e.target.value as Severity })}
              >
                {severities.map((s) => (
                  <option key={s} value={s}>{s}</option>
                ))}
              </select>
              <span className={styles.hint}>Findings of this severity or higher block; Unknown never does.</span>
            </div>
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="check-max-age">Max scan age (hours)</label>
              <input
                id="check-max-age"
                className={styles.input}
                type="number"
                min={1}
                step="any"
                value={maxAgeText}
                onChange={(e) => {
                  setMaxAgeText(e.target.value)
                  setCheck({ ...check, maxAgeSeconds: scanAgeSeconds(e.target.value) })
                }}
                placeholder="Twice the scanning interval"
              />
              {maxAgeInvalid ? (
                <span className={styles.fieldError}>At least 1 hour.</span>
              ) : (
                <span className={styles.hint}>Optional; older scans hold promotion like a missing one.</span>
              )}
            </div>
          </div>
          <label className={styles.checkRow}>
            <input
              type="checkbox"
              checked={!!check.ignoreUnfixed}
              onChange={(e) => setCheck({ ...check, ignoreUnfixed: e.target.checked })}
            />
            Ignore unfixed — findings that no version fixes yet do not block
          </label>
          <span className={styles.hint}>
            Kokumi scans only the images it vendors, so the check needs the Product's Vendor image policy and scanning
            enabled in the Kitchen. An image that is not vendored counts as scan missing and holds promotion too.
            {productPolicy && productPolicy !== 'Vendor' && ` This Product's image policy is ${productPolicy}, so none of its images are scanned.`}
          </span>
        </>
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
