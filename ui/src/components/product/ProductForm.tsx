import { useEffect, useRef, useState, type ReactNode } from 'react'
import { load, dump } from 'js-yaml'
import { Button, CloseIcon, Disclosure, IconButton, PlusIcon, SegmentedControl, TabButtons } from '../ui'
import YamlEditor from '../shared/YamlEditor'
import DestinationEditor from '../shared/DestinationEditor'
import HelmValuesEditor from '../shared/HelmValuesEditor'
import PatchesEditor, { TargetFields } from '../shared/PatchesEditor'
import PreviewTab from '../shared/PreviewTab'
import type {
  AllowedPatchTarget,
  ChartInfo,
  FileLayout,
  HelmRender,
  ImageFieldSpec,
  ImagePolicy,
  OCIDestination,
  OCISource,
  OverridePolicy,
  Overrides,
  Product,
  ProductFormData,
  ProductImages,
  ProductPreviewRequest,
  Render,
  SourceVerify,
} from '../../api/types'
import { emptyProductForm, imagePolicies, overridePolicies, productToFormData } from '../../api/types'
import { getChartInfo, listOCITags, previewProduct } from '../../api/client'
import { cleanTags } from '../../api/ociTags'
import { usePantries } from '../../hooks/usePantries'
import { assertKnownKeys, patchesFromYaml, patchesToYaml } from '../../utils/yaml'
import { describeImagePolicy, describePatchesPolicy, describeValuesPolicy } from '../../utils/products'
import styles from '../shared/Form.module.css'

interface Props {
  /** When provided the form is in "edit" mode. */
  product?: Product
  onCancel: () => void
  onSubmit: (data: ProductFormData) => Promise<void>
  /** Reports whether the form holds unsaved changes. */
  onDirtyChange?: (dirty: boolean) => void
}

type Tab = 'form' | 'yaml' | 'preview'

// ── YAML serialisation helpers ────────────────────────────────────────────────

function parseList(text: string): string[] {
  return [...new Set(text.split(',').map((s) => s.trim()).filter(Boolean))]
}

function isMapping(v: unknown): v is Record<string, unknown> {
  return !!v && typeof v === 'object' && !Array.isArray(v)
}

function destinationToYaml(dest: OCIDestination): Record<string, unknown> {
  if (dest.pantryRef?.name) return { pantryRef: { name: dest.pantryRef.name } }
  if (dest.oci) return { oci: dest.oci }
  return {}
}

/** The verification of a source as written in YAML, in the shape of the Product spec. */
function verifyToYaml(v: SourceVerify): Record<string, unknown> {
  const doc: Record<string, unknown> = {}
  if (v.secretRef) doc.secretRef = { name: v.secretRef }
  if (v.matchOIDCIdentity?.length) doc.matchOIDCIdentity = v.matchOIDCIdentity.map((m) => ({ issuer: m.issuer, subject: m.subject }))
  if (v.trustedRootSecretRef) doc.trustedRootSecretRef = { name: v.trustedRootSecretRef }
  return doc
}

function verifyFromYaml(raw: unknown): SourceVerify | undefined {
  if (raw === undefined || raw === null) return undefined
  if (!isMapping(raw)) throw new Error('source.verify must be a mapping')
  assertKnownKeys(raw, ['provider', 'secretRef', 'matchOIDCIdentity', 'trustedRootSecretRef'], 'source.verify')
  if (raw.provider !== undefined && raw.provider !== 'cosign') throw new Error('source.verify.provider must be cosign')
  const name = (ref: unknown, path: string): string | undefined => {
    if (ref === undefined || ref === null) return undefined
    if (!isMapping(ref)) throw new Error(`${path} must be a mapping with a name`)
    assertKnownKeys(ref, ['name'], path)
    return String(ref.name ?? '')
  }
  const identities = raw.matchOIDCIdentity
  if (identities !== undefined && identities !== null && !Array.isArray(identities)) {
    throw new Error('source.verify.matchOIDCIdentity must be a list')
  }
  return {
    secretRef: name(raw.secretRef, 'source.verify.secretRef'),
    matchOIDCIdentity: (identities ?? []).map((m: unknown, i: number) => {
      const entry = isMapping(m) ? m : {}
      assertKnownKeys(entry, ['issuer', 'subject'], `source.verify.matchOIDCIdentity[${i}]`)
      return { issuer: String(entry.issuer ?? ''), subject: String(entry.subject ?? '') }
    }),
    trustedRootSecretRef: name(raw.trustedRootSecretRef, 'source.verify.trustedRootSecretRef'),
  }
}

/** Why the verification cannot be saved: it needs exactly one of keys or identities. */
function verifyError(v?: SourceVerify): string | null {
  if (!v) return null
  const keyed = !!v.secretRef?.trim()
  const keyless = (v.matchOIDCIdentity?.length ?? 0) > 0
  if (keyed === keyless) return 'Verify the source by a key Secret or by signer identities'
  if (v.matchOIDCIdentity?.some((m) => !m.issuer.trim() || !m.subject.trim())) {
    return 'Every signer identity needs an issuer and a subject'
  }
  return null
}

function formToYaml(data: ProductFormData): string {
  const source: Record<string, unknown> = data.source.pantryRef
    ? { pantryRef: { name: data.source.pantryRef.name }, version: data.source.version }
    : { oci: data.source.oci ?? '', version: data.source.version }
  if (data.source.verify) source.verify = verifyToYaml(data.source.verify)
  const doc: Record<string, unknown> = { source }
  if (data.vendor) doc.vendor = destinationToYaml(data.vendor)
  if (data.render?.helm) {
    const h = data.render.helm
    const helmDoc: Record<string, unknown> = {}
    if (h.releaseName) helmDoc.releaseName = h.releaseName
    if (h.namespace) helmDoc.namespace = h.namespace
    if (h.includeCRDs) helmDoc.includeCRDs = true
    if (h.values && Object.keys(h.values).length > 0) helmDoc.values = h.values
    doc.render = { helm: helmDoc }
  } else if (data.render?.manifest) {
    doc.render = { manifest: { layout: data.render.manifest.layout ?? 'Single' } }
  }
  if (data.patches.length > 0) doc.patches = patchesToYaml(data.patches)
  doc.overrides = overridesToYaml(data.overrides)
  const images = imagesToYaml(data.images)
  if (images) doc.images = images
  return dump(doc, { lineWidth: 100 })
}

/** The image settings as written in YAML; absent when they are the default. */
function imagesToYaml(i: ProductImages): Record<string, unknown> | undefined {
  const doc: Record<string, unknown> = {}
  if (i.policy !== 'Record') doc.policy = i.policy
  if (i.fieldSpecs?.length) {
    doc.fieldSpecs = i.fieldSpecs.map((f) => ({
      ...(f.group ? { group: f.group } : {}),
      ...(f.version ? { version: f.version } : {}),
      kind: f.kind,
      path: f.path,
    }))
  }
  if (i.pullSecretRef?.name) doc.pullSecretRef = { name: i.pullSecretRef.name }
  return Object.keys(doc).length ? doc : undefined
}

function imagesFromYaml(raw: unknown): ProductImages {
  if (raw === undefined || raw === null) return { policy: 'Record' }
  if (!isMapping(raw)) throw new Error('images must be a mapping')
  assertKnownKeys(raw, ['policy', 'fieldSpecs', 'pullSecretRef'], 'images')
  const policy = imagePolicies.find((p) => p === (raw.policy ?? 'Record'))
  if (!policy) throw new Error('images.policy must be Record, Pin or Vendor')
  const images: ProductImages = { policy }
  if (raw.fieldSpecs !== undefined && raw.fieldSpecs !== null) {
    if (!Array.isArray(raw.fieldSpecs)) throw new Error('images.fieldSpecs must be a list')
    images.fieldSpecs = raw.fieldSpecs.map((f: unknown, i: number) => {
      const entry = isMapping(f) ? f : {}
      assertKnownKeys(entry, ['group', 'version', 'kind', 'path'], `images.fieldSpecs[${i}]`)
      return {
        group: entry.group ? String(entry.group) : undefined,
        version: entry.version ? String(entry.version) : undefined,
        kind: String(entry.kind ?? ''),
        path: String(entry.path ?? ''),
      }
    })
  }
  if (raw.pullSecretRef !== undefined && raw.pullSecretRef !== null) {
    if (!isMapping(raw.pullSecretRef)) throw new Error('images.pullSecretRef must be a mapping with a name')
    assertKnownKeys(raw.pullSecretRef, ['name'], 'images.pullSecretRef')
    images.pullSecretRef = { name: String(raw.pullSecretRef.name ?? '') }
  }
  return images
}

/** Why the image settings cannot be saved: every field spec needs a kind and a path. */
function imagesError(i: ProductImages): string | null {
  if (i.fieldSpecs?.some((f) => !f.kind.trim() || !f.path.trim())) {
    return 'Every image field needs a kind and a path'
  }
  return null
}

/** The overrides as written in YAML; the lists only appear with policy Listed. */
function overridesToYaml(o: Overrides): Record<string, unknown> {
  const { values, patches } = o
  return {
    values: values.policy === 'Listed' ? { policy: values.policy, paths: values.paths ?? [] } : { policy: values.policy },
    patches: patches.policy === 'Listed'
      ? {
          policy: patches.policy,
          targets: (patches.targets ?? []).map((a) => ({
            target: {
              kind: a.target.kind,
              name: a.target.name,
              ...(a.target.namespace ? { namespace: a.target.namespace } : {}),
            },
            paths: a.paths,
          })),
        }
      : { policy: patches.policy },
  }
}

/**
 * Reads one overrides category: its policy, which defaults to None, and its
 * list, which may only appear with policy Listed.
 */
function overrideCategoryFromYaml(raw: unknown, list: string, path: string): { policy: OverridePolicy; items?: unknown[] } {
  if (raw === undefined || raw === null) return { policy: 'None' }
  if (!isMapping(raw)) throw new Error(`${path} must be a mapping with a policy of None, Listed or All`)
  assertKnownKeys(raw, ['policy', list], path)
  const policy = overridePolicies.find((p) => p === (raw.policy ?? 'None'))
  if (!policy) throw new Error(`${path}.policy must be None, Listed or All`)
  const items = raw[list]
  if (items === undefined || items === null) return { policy }
  if (policy !== 'Listed') throw new Error(`${path}.${list} is only allowed with policy Listed`)
  if (!Array.isArray(items)) throw new Error(`${path}.${list} must be a list`)
  return { policy, items }
}

function overridesFromYaml(raw: unknown): Overrides {
  if (raw !== undefined && raw !== null && !isMapping(raw)) throw new Error('overrides must be a mapping')
  const o = isMapping(raw) ? raw : {}
  assertKnownKeys(o, ['values', 'patches'], 'overrides')

  const values = overrideCategoryFromYaml(o.values, 'paths', 'overrides.values')
  const patches = overrideCategoryFromYaml(o.patches, 'targets', 'overrides.patches')

  return {
    values: values.policy === 'Listed'
      ? { policy: values.policy, paths: (values.items ?? []).map(String) }
      : { policy: values.policy },
    patches: patches.policy === 'Listed'
      ? {
          policy: patches.policy,
          targets: (patches.items ?? []).map((a, i) => {
            const at = `overrides.patches.targets[${i}]`
            const entry = isMapping(a) ? a : {}
            assertKnownKeys(entry, ['target', 'paths'], at)
            const target = (isMapping(entry.target) ? entry.target : {}) as Record<string, string>
            assertKnownKeys(target, ['kind', 'name', 'namespace'], `${at}.target`)
            return {
              target: { kind: target.kind ?? '', name: target.name ?? '', namespace: target.namespace },
              paths: Array.isArray(entry.paths) ? entry.paths.map(String) : [],
            }
          }),
        }
      : { policy: patches.policy },
  }
}

/** Why the overrides cannot be saved: a Listed policy needs something listed. */
function overridesError(o: Overrides): string | null {
  if (o.values.policy === 'Listed' && !o.values.paths?.length) {
    return 'List at least one Helm value path, or pick another policy for values'
  }
  if (o.patches.policy === 'Listed' && !o.patches.targets?.length) {
    return 'List at least one patch target, or pick another policy for patches and edits'
  }
  return null
}

function yamlToPartialForm(text: string): Omit<ProductFormData, 'name' | 'namespace'> {
  const doc = load(text) as Record<string, unknown>
  if (!isMapping(doc)) throw new Error('YAML must be a mapping')
  assertKnownKeys(doc, ['source', 'vendor', 'render', 'patches', 'overrides', 'images'], '')

  // Source — exactly one of oci or pantryRef
  const src = isMapping(doc.source) ? doc.source : {}
  assertKnownKeys(src, ['oci', 'pantryRef', 'version', 'verify'], 'source')
  const srcPantryRef = src.pantryRef as Record<string, string> | undefined
  const version = String(src.version ?? '')
  const verify = verifyFromYaml(src.verify)
  const source: OCISource = srcPantryRef?.name
    ? { pantryRef: { name: srcPantryRef.name }, version, verify }
    : { oci: String(src.oci ?? ''), version, verify }

  // Vendor — present means vendoring; {} is the in-cluster registry
  let vendor: OCIDestination | undefined
  if (isMapping(doc.vendor)) {
    assertKnownKeys(doc.vendor, ['oci', 'pantryRef'], 'vendor')
    const vendorPantryRef = doc.vendor.pantryRef as Record<string, string> | undefined
    vendor = vendorPantryRef?.name
      ? { pantryRef: { name: vendorPantryRef.name } }
      : doc.vendor.oci ? { oci: String(doc.vendor.oci) } : {}
  }

  const rawRender = isMapping(doc.render) ? doc.render : undefined
  assertKnownKeys(rawRender, ['helm', 'manifest'], 'render')
  if (rawRender?.helm && rawRender.manifest) throw new Error('render.helm and render.manifest are mutually exclusive')
  let render: Render | undefined
  if (rawRender?.helm) {
    const h = isMapping(rawRender.helm) ? rawRender.helm : {}
    assertKnownKeys(h, ['releaseName', 'namespace', 'includeCRDs', 'values'], 'render.helm')
    render = {
      helm: {
        releaseName: (h.releaseName as string) ?? '',
        namespace: (h.namespace as string) ?? '',
        includeCRDs: Boolean(h.includeCRDs),
        values: isMapping(h.values) ? h.values : {},
      },
    }
  } else if (rawRender?.manifest) {
    const m = isMapping(rawRender.manifest) ? rawRender.manifest : {}
    assertKnownKeys(m, ['layout'], 'render.manifest')
    render = { manifest: { layout: m.layout === 'Multi' ? 'Multi' : 'Single' } }
  }

  // Overrides — absent policies are None, as on the server
  const overrides = overridesFromYaml(doc.overrides)

  return { source, vendor, render, patches: patchesFromYaml(doc.patches), overrides, images: imagesFromYaml(doc.images) }
}

function previewRequest(data: ProductFormData): ProductPreviewRequest | null {
  if (!(data.source.oci || data.source.pantryRef?.name) || !data.source.version) return null
  return { namespace: data.namespace, name: data.name, source: data.source, render: data.render, patches: data.patches, images: data.images }
}

// ── Main component ────────────────────────────────────────────────────────────

export default function ProductForm({ product, onCancel, onSubmit, onDirtyChange }: Props) {
  const isEdit = !!product
  const [tab, setTab] = useState<Tab>('form')
  const [formData, setFormData] = useState<ProductFormData>(() =>
    product ? productToFormData(product) : emptyProductForm(),
  )
  const [yamlText, setYamlText] = useState(() => formToYaml(formData))
  const [yamlError, setYamlError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)

  const [initialYaml] = useState(() => formToYaml(formData))
  const [initialIdentity] = useState(() => `${formData.namespace}/${formData.name}`)

  // ── Tab switching ──────────────────────────────────────────────────────────

  function selectTab(next: Tab) {
    if (next === 'yaml') {
      setYamlText(formToYaml(formData))
      setYamlError(null)
      setTab('yaml')
      return
    }
    if (tab === 'yaml') {
      try {
        const partial = yamlToPartialForm(yamlText)
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

  async function handleSubmit() {
    let data = formData
    if (tab === 'yaml') {
      try {
        data = { ...formData, ...yamlToPartialForm(yamlText) }
      } catch (e) {
        setYamlError(e instanceof Error ? e.message : String(e))
        return
      }
    }
    const invalid = overridesError(data.overrides) ?? verifyError(data.source.verify) ?? imagesError(data.images)
    if (invalid) {
      setSubmitError(invalid)
      return
    }
    if (data.source.verify) {
      // Empty optional fields are sent as absent.
      const { secretRef, matchOIDCIdentity, trustedRootSecretRef } = data.source.verify
      data = {
        ...data,
        source: {
          ...data.source,
          verify: {
            secretRef: secretRef?.trim() || undefined,
            matchOIDCIdentity: matchOIDCIdentity?.length ? matchOIDCIdentity.map((m) => ({ issuer: m.issuer.trim(), subject: m.subject.trim() })) : undefined,
            trustedRootSecretRef: trustedRootSecretRef?.trim() || undefined,
          },
        },
      }
    }
    setSaving(true)
    setSubmitError(null)
    try {
      await onSubmit(data)
    } catch (e) {
      setSubmitError((e as Error).message || 'Failed to save Product')
    } finally {
      setSaving(false)
    }
  }

  // ── Render ─────────────────────────────────────────────────────────────────

  let isDirty: boolean
  if (tab === 'yaml') {
    try {
      isDirty = formToYaml({ ...formData, ...yamlToPartialForm(yamlText) }) !== initialYaml
    } catch {
      isDirty = true
    }
  } else {
    isDirty = formToYaml(formData) !== initialYaml
  }
  // Name/namespace are not part of the YAML view but still count as changes.
  if (`${formData.namespace}/${formData.name}` !== initialIdentity) isDirty = true

  useEffect(() => {
    onDirtyChange?.(isDirty)
  }, [isDirty, onDirtyChange])

  return (
    <>
      <TabButtons<Tab>
        tabs={[
          { id: 'form', label: 'Form' },
          { id: 'yaml', label: 'YAML' },
          { id: 'preview', label: 'Preview' },
        ]}
        active={tab}
        onSelect={selectTab}
      />

      <div className={`${styles.formCard} ${styles.tabContent}`}>
        {tab === 'form' && (
          <FormView
            formData={formData}
            isEdit={isEdit}
            onChange={(update) => setFormData((prev) => ({ ...prev, ...update(prev) }))}
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
            render={previewProduct}
            placeholder="Fill in the source and version to preview the rendered manifest."
          />
        )}
      </div>

      {submitError && <p className={styles.yamlError}>{submitError}</p>}
      <div className={styles.formActions}>
        <Button variant="secondary" onClick={onCancel} disabled={saving}>Cancel</Button>
        <Button variant="primary" onClick={handleSubmit} disabled={saving || (isEdit && !isDirty)}>
          {saving ? 'Saving…' : isEdit ? 'Save changes' : 'Create Product'}
        </Button>
      </div>
    </>
  )
}

// ── FormView ──────────────────────────────────────────────────────────────────

interface FormViewProps {
  formData: ProductFormData
  isEdit: boolean
  /** Merges the fields returned by update into the form data. */
  onChange: (update: (prev: ProductFormData) => Partial<ProductFormData>) => void
}

const emptyHelm = (): HelmRender => ({ releaseName: '', namespace: '', includeCRDs: false, values: {} })

function FormView({ formData, isEdit, onChange }: FormViewProps) {
  const pantries = usePantries()

  const [isVendorOpen, setIsVendorOpen] = useState(!!formData.vendor)
  const [isVerifyOpen, setIsVerifyOpen] = useState(!!formData.source.verify)
  const [isImagesOpen, setIsImagesOpen] = useState(!!imagesToYaml(formData.images))

  // Source type: 'oci' = direct URL, 'pantry' = named Pantry provides the URL
  const [sourceMode, setSourceMode] = useState<'oci' | 'pantry'>(
    formData.source.pantryRef ? 'pantry' : 'oci',
  )

  const [versionTags, setVersionTags] = useState<string[]>([])
  const [versionTagsLoading, setVersionTagsLoading] = useState(false)
  const lastFetchedTagsRef = useRef<string>('')
  const tagsSeqRef = useRef(0)

  const [chartInfo, setChartInfo] = useState<ChartInfo | null>(null)
  const [chartInfoLoading, setChartInfoLoading] = useState(false)
  const lastFetchedChartRef = useRef<string>('')
  const chartSeqRef = useRef(0)

  function setField<K extends keyof ProductFormData>(key: K, val: ProductFormData[K]) {
    const update: Partial<ProductFormData> = {}
    update[key] = val
    onChange(() => update)
  }

  // Keeps an existing Helm configuration, so a late chart lookup cannot reset it.
  function enableHelm() {
    onChange((prev) => (prev.render?.helm ? {} : { render: { helm: emptyHelm() } }))
  }

  // Values only apply to Helm, so their overrides go with it.
  function disableHelm() {
    onChange((prev) => ({
      render: undefined,
      overrides: { ...prev.overrides, values: { policy: 'None' } },
    }))
  }

  function updateHelm(h: HelmRender) {
    setField('render', { helm: h })
  }

  function setLayout(layout: FileLayout) {
    // Single is the default, so it is left out.
    setField('render', layout === 'Single' ? undefined : { manifest: { layout } })
  }

  // Build the OCI ref for the current source — resolves pantry URL when in pantry mode.
  function resolveOciRef(): string {
    if (formData.source.pantryRef?.name) {
      const p = (pantries ?? []).find(
        (p) => p.name === formData.source.pantryRef!.name && p.namespace === formData.namespace,
      )
      return p?.url ?? ''
    }
    return formData.source.oci ?? ''
  }

  function fetchTags(oci: string, pantryName?: string, pantryNs?: string) {
    const resolvedPantryName = pantryName ?? formData.source.pantryRef?.name
    const resolvedPantryNs = pantryNs ?? formData.namespace
    const dedupeKey = `${oci}|${resolvedPantryName ?? ''}`
    if (!oci || dedupeKey === lastFetchedTagsRef.current) return
    lastFetchedTagsRef.current = dedupeKey
    const seq = ++tagsSeqRef.current
    setVersionTagsLoading(true)
    listOCITags(oci, resolvedPantryName, resolvedPantryNs)
      .then((tags) => {
        if (tagsSeqRef.current !== seq) return
        // Drop cosign signature/attestation tags and sort semver-desc so the
        // newest release appears first, matching the Pantry tag browser.
        setVersionTags(cleanTags(tags))
      })
      .catch(() => { /* non-blocking: keep previous tags */ })
      .finally(() => { if (tagsSeqRef.current === seq) setVersionTagsLoading(false) })
  }

  function fetchChartInfo(oci: string, version: string) {
    const key = `${oci}@${version}`
    if (!oci || !version || key === lastFetchedChartRef.current) return
    lastFetchedChartRef.current = key
    const seq = ++chartSeqRef.current
    setChartInfoLoading(true)
    getChartInfo(oci, version, formData.source.pantryRef?.name, formData.namespace)
      .then((info) => {
        if (chartSeqRef.current !== seq) return
        setChartInfo(info)
        // Auto-enable Helm rendering when the source is identified as a Helm
        // chart and Helm rendering has not been configured yet. Never auto-disable.
        if (info.isHelm) enableHelm()
      })
      .catch(() => { if (chartSeqRef.current === seq) setChartInfo(null) })
      .finally(() => { if (chartSeqRef.current === seq) setChartInfoLoading(false) })
  }

  // On mount: load tags and chart info immediately if the source is already
  // populated (edit mode).
  useEffect(() => {
    const oci = formData.source.oci ?? ''
    const version = formData.source.version
    if (oci) fetchTags(oci)
    if (oci && version) fetchChartInfo(oci, version)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function handleOciBlur() {
    const oci = resolveOciRef()
    fetchTags(oci)
    if (formData.source.version) fetchChartInfo(oci, formData.source.version)
  }

  function handleVersionSelect(version: string) {
    setField('source', { ...formData.source, version })
    const oci = resolveOciRef()
    if (oci) fetchChartInfo(oci, version)
  }

  const verify = formData.source.verify
  const verifySummary = verify
    ? verify.secretRef
      ? `keys in ${verify.secretRef}`
      : `${verify.matchOIDCIdentity?.length ?? 0} signer identit${(verify.matchOIDCIdentity?.length ?? 0) === 1 ? 'y' : 'ies'}`
    : undefined
  const imageFields = formData.images.fieldSpecs?.length ?? 0
  const imagesSummary = imageFields
    ? `${formData.images.policy}, ${imageFields} extra field${imageFields === 1 ? '' : 's'}`
    : formData.images.policy
  const vendorSummary = formData.vendor &&
    (formData.vendor.oci || (formData.vendor.pantryRef?.name ? `pantry: ${formData.vendor.pantryRef.name}` : 'in-cluster registry'))

  return (
    <div className={styles.formGrid}>
      {/* Name + Namespace */}
      <div className={styles.row2}>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Name</label>
          <input
            className={`${styles.input} ${isEdit ? styles.inputDisabled : ''}`}
            value={formData.name}
            onChange={(e) => setField('name', e.target.value)}
            readOnly={isEdit}
            placeholder="my-app"
          />
        </div>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Namespace</label>
          <input
            className={`${styles.input} ${isEdit ? styles.inputDisabled : ''}`}
            value={formData.namespace}
            onChange={(e) => setField('namespace', e.target.value)}
            readOnly={isEdit}
            placeholder="kokumi"
          />
        </div>
      </div>

      {/* Source */}
      <div className={styles.fieldGroup}>
        <p className={styles.sectionTitle}>Source</p>
        <SegmentedControl
          aria-label="Source"
          segments={[
            { id: 'oci', label: 'Direct OCI URL' },
            { id: 'pantry', label: 'From Pantry' },
          ]}
          value={sourceMode}
          onChange={(m) => {
            setSourceMode(m)
            setField(
              'source',
              m === 'oci'
                ? { oci: '', version: formData.source.version, verify: formData.source.verify }
                : { pantryRef: { name: '' }, version: formData.source.version, verify: formData.source.verify },
            )
          }}
        />
      </div>
      <div className={styles.row2}>
        <div className={styles.fieldGroup}>
          {sourceMode === 'oci' ? (
            <>
              <label className={styles.label}>OCI URL</label>
              <input
                className={styles.input}
                value={formData.source.oci ?? ''}
                onChange={(e) => setField('source', { ...formData.source, oci: e.target.value })}
                onBlur={handleOciBlur}
                placeholder="oci://ghcr.io/my-org/charts/app"
              />
            </>
          ) : (
            <>
              <label className={styles.label}>Pantry</label>
              <select
                className={styles.input}
                value={formData.source.pantryRef?.name ?? ''}
                onChange={(e) => {
                  const name = e.target.value
                  setField('source', { pantryRef: { name }, version: formData.source.version, verify: formData.source.verify })
                  if (name) {
                    const p = (pantries ?? []).find((p) => p.name === name && p.namespace === formData.namespace)
                    if (p?.url) fetchTags(p.url, name, formData.namespace)
                  }
                }}
              >
                <option value="">— select a pantry —</option>
                {(pantries ?? []).filter((p) => p.namespace === formData.namespace).map((p) => (
                  <option key={p.name} value={p.name}>{p.name}</option>
                ))}
              </select>
            </>
          )}
        </div>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Version</label>
          <VersionPicker
            value={formData.source.version}
            tags={versionTags}
            loading={versionTagsLoading}
            onChange={handleVersionSelect}
          />
        </div>
      </div>

      {/* Renderer */}
      <div>
        <p className={styles.sectionTitle}>Renderer</p>
        <label className={styles.checkRow}>
          <input
            type="checkbox"
            checked={!!formData.render?.helm}
            onChange={(e) => (e.target.checked ? enableHelm() : disableHelm())}
          />
          Render with Helm
        </label>
        {formData.render?.helm ? (
          <div className={styles.helmSection}>
            <HelmEditor
              helm={formData.render.helm}
              onUpdate={updateHelm}
              chartInfo={chartInfo}
              chartInfoLoading={chartInfoLoading}
            />
          </div>
        ) : (
          <div className={styles.fieldGroup} style={{ marginTop: 10 }}>
            <label className={styles.label}>Manifest files</label>
            <select
              className={styles.input}
              value={formData.render?.manifest?.layout ?? 'Single'}
              onChange={(e) => setLayout(e.target.value as FileLayout)}
            >
              <option value="Single">Single manifest file</option>
              <option value="Multi">Keep separate files</option>
            </select>
          </div>
        )}
      </div>

      {/* Base patches */}
      <div>
        <p className={styles.sectionTitle}>Base Patches</p>
        <PatchesEditor patches={formData.patches} onChange={(p) => setField('patches', p)} />
      </div>

      {/* Overrides */}
      <div>
        <p className={styles.sectionTitle}>Overrides</p>
        <OverridesEditor
          overrides={formData.overrides}
          helm={!!formData.render?.helm}
          onChange={(o) => setField('overrides', o)}
        />
      </div>

      {/* Images (collapsible) */}
      <Disclosure label="Images" summary={imagesSummary} open={isImagesOpen} onOpenChange={setIsImagesOpen}>
        <ImagesEditor images={formData.images} onChange={(images) => setField('images', images)} />
      </Disclosure>

      {/* Source verification (collapsible) */}
      <Disclosure label="Verify source" summary={verifySummary} open={isVerifyOpen} onOpenChange={setIsVerifyOpen}>
        <VerifyEditor
          verify={formData.source.verify}
          onChange={(verify) => setField('source', { ...formData.source, verify })}
        />
      </Disclosure>

      {/* Vendor (collapsible) */}
      <Disclosure label="Vendor" summary={vendorSummary} open={isVendorOpen} onOpenChange={setIsVendorOpen}>
        <div className={styles.formGrid} style={{ gap: 10, marginTop: 4 }}>
          <label className={styles.checkRow}>
            <input
              type="checkbox"
              checked={!!formData.vendor}
              onChange={(e) => setField('vendor', e.target.checked ? {} : undefined)}
            />
            Copy the source into another registry — Orders render from the copy
          </label>
          {formData.vendor && (
            <DestinationEditor
              destination={formData.vendor}
              onChange={(dest) => setField('vendor', dest)}
              namespace={formData.namespace}
              name={`vendor/${formData.name || 'name'}`}
            />
          )}
        </div>
      </Disclosure>
    </div>
  )
}

// ── ImagesEditor ──────────────────────────────────────────────────────────────

interface ImagesEditorProps {
  images: ProductImages
  onChange: (images: ProductImages) => void
}

/**
 * ImagesEditor edits how the container images of the rendered manifests are
 * handled: the policy, the custom resource fields that hold images, and the
 * Secret whose credentials resolve and copy them.
 */
function ImagesEditor({ images, onChange }: ImagesEditorProps) {
  const fieldSpecs = images.fieldSpecs ?? []

  function setFieldSpecs(next: ImageFieldSpec[]) {
    onChange({ ...images, fieldSpecs: next.length ? next : undefined })
  }

  function setFieldSpec(index: number, change: Partial<ImageFieldSpec>) {
    setFieldSpecs(fieldSpecs.map((f, i) => (i === index ? { ...f, ...change } : f)))
  }

  return (
    <div className={styles.formGrid} style={{ gap: 10, marginTop: 4 }}>
      <div className={styles.fieldGroup}>
        <label className={styles.label}>Policy</label>
        <select
          className={styles.input}
          value={images.policy}
          onChange={(e) => onChange({ ...images, policy: e.target.value as ImagePolicy })}
        >
          {imagePolicies.map((p) => (
            <option key={p} value={p}>{p}</option>
          ))}
        </select>
        <span className={styles.hint}>
          {describeImagePolicy(images.policy)}.
          {images.policy === 'Vendor' && ' Nodes must be able to pull from each Order\'s destination registry.'}
        </span>
      </div>

      <div className={styles.fieldGroup}>
        <label className={styles.label}>Image fields of custom resources</label>
        <span className={styles.hint}>
          Containers, init containers, ephemeral containers and image volumes are always searched.
          Add the fields of custom resources that hold an image, e.g. kind <code>Prometheus</code>,
          path <code>spec/image</code>.
        </span>
        {fieldSpecs.map((f, i) => (
          <div key={i} className={styles.row2}>
            <div className={styles.row2}>
              <input
                className={styles.input}
                aria-label="Group"
                value={f.group ?? ''}
                onChange={(e) => setFieldSpec(i, { group: e.target.value || undefined })}
                placeholder="group, e.g. monitoring.coreos.com"
              />
              <input
                className={styles.input}
                aria-label="Kind"
                value={f.kind}
                onChange={(e) => setFieldSpec(i, { kind: e.target.value })}
                placeholder="Kind"
              />
            </div>
            <div style={{ display: 'flex', gap: 6 }}>
              <input
                className={styles.input}
                aria-label="Path"
                value={f.path}
                onChange={(e) => setFieldSpec(i, { path: e.target.value })}
                placeholder="spec/image"
              />
              <IconButton aria-label="Remove image field" onClick={() => setFieldSpecs(fieldSpecs.filter((_, j) => j !== i))}>
                <CloseIcon />
              </IconButton>
            </div>
          </div>
        ))}
        <div>
          <Button variant="secondary" onClick={() => setFieldSpecs([...fieldSpecs, { kind: '', path: '' }])}>
            <PlusIcon /> Add image field
          </Button>
        </div>
      </div>

      <div className={styles.fieldGroup}>
        <label className={styles.label}>Pull Secret (optional)</label>
        <input
          className={styles.input}
          value={images.pullSecretRef?.name ?? ''}
          onChange={(e) => onChange({ ...images, pullSecretRef: e.target.value ? { name: e.target.value } : undefined })}
          placeholder="registry-credentials"
        />
        <span className={styles.hint}>
          A <code>kubernetes.io/dockerconfigjson</code> Secret in the Product&apos;s namespace. Its
          credentials are used per registry to resolve and vendor the images; without it,
          registries are read anonymously.
        </span>
      </div>
    </div>
  )
}

// ── VerifyEditor ──────────────────────────────────────────────────────────────

type VerifyMode = 'none' | 'keys' | 'keyless'

function verifyMode(v?: SourceVerify): VerifyMode {
  if (!v) return 'none'
  return v.matchOIDCIdentity?.length ? 'keyless' : 'keys'
}

interface VerifyEditorProps {
  verify?: SourceVerify
  onChange: (v: SourceVerify | undefined) => void
}

/**
 * VerifyEditor edits how the source's signature is verified: by the public
 * keys in a Secret, or keyless against expected signer identities, as Flux's
 * OCIRepository does.
 */
function VerifyEditor({ verify, onChange }: VerifyEditorProps) {
  const mode = verifyMode(verify)
  const identities = verify?.matchOIDCIdentity ?? []

  function setIdentity(index: number, change: Partial<{ issuer: string; subject: string }>) {
    onChange({ ...verify, matchOIDCIdentity: identities.map((m, i) => (i === index ? { ...m, ...change } : m)) })
  }

  return (
    <div className={styles.formGrid} style={{ gap: 10, marginTop: 4 }}>
      <SegmentedControl
        aria-label="Verify source"
        segments={[
          { id: 'none', label: 'Not required' },
          { id: 'keys', label: 'Public keys' },
          { id: 'keyless', label: 'Keyless' },
        ]}
        value={mode}
        onChange={(m) => {
          if (m === 'none') onChange(undefined)
          else if (m === 'keys') onChange({ secretRef: '' })
          else onChange({ matchOIDCIdentity: [{ issuer: '', subject: '' }] })
        }}
      />
      {mode === 'none' && (
        <span className={styles.hint}>
          A source signed by a key the Kitchen trusts still verifies. When the
          Kitchen requires verification, an unverified source is not rendered.
        </span>
      )}
      {mode === 'keys' && (
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Secret with public keys</label>
          <input
            className={styles.input}
            value={verify?.secretRef ?? ''}
            onChange={(e) => onChange({ secretRef: e.target.value })}
            placeholder="podinfo-keys"
          />
          <span className={styles.hint}>
            A Secret in the Product&apos;s namespace. Every key ending in <code>.pub</code> is
            tried; one verifying signature suffices.
          </span>
        </div>
      )}
      {mode === 'keyless' && (
        <>
          {identities.map((m, i) => (
            <div key={i} className={styles.row2}>
              <div className={styles.fieldGroup}>
                <label className={styles.label}>Issuer</label>
                <input
                  className={styles.input}
                  value={m.issuer}
                  onChange={(e) => setIdentity(i, { issuer: e.target.value })}
                  placeholder="^https://token\.actions\.githubusercontent\.com$"
                />
              </div>
              <div className={styles.fieldGroup}>
                <label className={styles.label}>Subject</label>
                <div style={{ display: 'flex', gap: 6 }}>
                  <input
                    className={styles.input}
                    value={m.subject}
                    onChange={(e) => setIdentity(i, { subject: e.target.value })}
                    placeholder="^https://github\.com/org/repo/\.github/workflows/release\.yml@refs/tags/.*$"
                  />
                  <IconButton
                    aria-label="Remove signer identity"
                    disabled={identities.length === 1}
                    onClick={() => onChange({ ...verify, matchOIDCIdentity: identities.filter((_, j) => j !== i) })}
                  >
                    <CloseIcon />
                  </IconButton>
                </div>
              </div>
            </div>
          ))}
          <div>
            <Button
              variant="secondary"
              onClick={() => onChange({ ...verify, matchOIDCIdentity: [...identities, { issuer: '', subject: '' }] })}
            >
              <PlusIcon /> Add signer identity
            </Button>
          </div>
          <span className={styles.hint}>
            Both fields are regular expressions; one matching signer suffices.
          </span>
          <div className={styles.fieldGroup}>
            <label className={styles.label}>Trusted root Secret (optional)</label>
            <input
              className={styles.input}
              value={verify?.trustedRootSecretRef ?? ''}
              onChange={(e) => onChange({ ...verify, trustedRootSecretRef: e.target.value || undefined })}
              placeholder="sigstore-root"
            />
            <span className={styles.hint}>
              Holds the Sigstore <code>trusted_root.json</code>. Without it the public
              Sigstore instance is used, which needs network access.
            </span>
          </div>
        </>
      )}
    </div>
  )
}

// ── HelmEditor ────────────────────────────────────────────────────────────────

interface HelmEditorProps {
  helm: HelmRender
  onUpdate: (h: HelmRender) => void
  chartInfo: ChartInfo | null
  chartInfoLoading: boolean
}

function HelmEditor({ helm, onUpdate, chartInfo, chartInfoLoading }: HelmEditorProps) {
  return (
    <div className={styles.helmCard}>
      <div className={styles.row2}>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Release Name</label>
          <input
            className={styles.input}
            value={helm.releaseName ?? ''}
            onChange={(e) => onUpdate({ ...helm, releaseName: e.target.value })}
            placeholder="defaults to Order name"
          />
        </div>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Namespace</label>
          <input
            className={styles.input}
            value={helm.namespace ?? ''}
            onChange={(e) => onUpdate({ ...helm, namespace: e.target.value })}
            placeholder="defaults to Order namespace"
          />
        </div>
      </div>

      <label className={styles.checkRow}>
        <input
          type="checkbox"
          checked={!!helm.includeCRDs}
          onChange={(e) => onUpdate({ ...helm, includeCRDs: e.target.checked })}
        />
        Include CRDs
      </label>

      <HelmValuesEditor
        values={helm.values ?? {}}
        onChange={(values) => onUpdate({ ...helm, values })}
        chartInfo={chartInfo}
        chartInfoLoading={chartInfoLoading}
      />
    </div>
  )
}

// ── OverridesEditor ───────────────────────────────────────────────────────────

interface OverridesEditorProps {
  overrides: Overrides
  /** Values only apply when the Product renders with Helm. */
  helm: boolean
  onChange: (o: Overrides) => void
}

function OverridesEditor({ overrides, helm, onChange }: OverridesEditorProps) {
  const { values, patches } = overrides
  const targets = patches.targets ?? []

  function setTargets(next: AllowedPatchTarget[]) {
    onChange({ ...overrides, patches: { policy: 'Listed', targets: next } })
  }

  return (
    <div className={styles.formGrid}>
      {helm && (
        <PolicyField
          label="Helm values"
          policy={values.policy}
          description={describeValuesPolicy(values.policy)}
          onChange={(policy) =>
            onChange({ ...overrides, values: policy === 'Listed' ? { policy, paths: [] } : { policy } })
          }
        >
          <ValuePathsField
            paths={values.paths ?? []}
            onChange={(paths) => onChange({ ...overrides, values: { policy: 'Listed', paths } })}
          />
        </PolicyField>
      )}

      <PolicyField
        label="Patches and edits"
        policy={patches.policy}
        description={describePatchesPolicy(patches.policy)}
        onChange={(policy) =>
          onChange({ ...overrides, patches: policy === 'Listed' ? { policy, targets: [] } : { policy } })
        }
      >
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Allowed patch targets</label>
          {targets.length === 0 && <span className={styles.hint}>Add at least one target.</span>}
          <div className={styles.patchList}>
            {targets.map((t, idx) => (
              <AllowedTargetEditor
                key={idx}
                index={idx}
                target={t}
                onUpdate={(next) => setTargets(targets.map((x, i) => (i === idx ? next : x)))}
                onRemove={() => setTargets(targets.filter((_, i) => i !== idx))}
              />
            ))}
          </div>
          <Button
            block
            className={styles.addPatch}
            icon={<PlusIcon />}
            onClick={() => setTargets([...targets, { target: { kind: '', name: '' }, paths: [''] }])}
          >
            Add patch target
          </Button>
        </div>
      </PolicyField>
    </div>
  )
}

interface PolicyFieldProps {
  label: string
  policy: OverridePolicy
  /** What the policy lets Orders do. */
  description: string
  onChange: (policy: OverridePolicy) => void
  /** The list editor, shown with policy Listed. */
  children: ReactNode
}

function PolicyField({ label, policy, description, onChange, children }: PolicyFieldProps) {
  return (
    <div className={styles.formGrid}>
      <div className={styles.fieldGroup}>
        <label className={styles.label}>{label}</label>
        <select className={styles.input} value={policy} onChange={(e) => onChange(e.target.value as OverridePolicy)}>
          {overridePolicies.map((p) => (
            <option key={p} value={p}>{p}</option>
          ))}
        </select>
        <span className={styles.hint}>{description}.</span>
      </div>
      {policy === 'Listed' && children}
    </div>
  )
}

/** Comma-separated value paths; mounts with policy Listed, so its text starts from the paths. */
function ValuePathsField({ paths, onChange }: { paths: string[]; onChange: (paths: string[]) => void }) {
  const [text, setText] = useState(paths.join(', '))
  return (
    <div className={styles.fieldGroup}>
      <label className={styles.label}>Allowed value paths</label>
      <input
        className={styles.input}
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          onChange(parseList(e.target.value))
        }}
        placeholder="ui.message, replicaCount"
      />
      <span className={styles.hint}>Comma-separated exact Helm value paths.</span>
    </div>
  )
}

interface AllowedTargetEditorProps {
  index: number
  target: AllowedPatchTarget
  onUpdate: (t: AllowedPatchTarget) => void
  onRemove: () => void
}

function AllowedTargetEditor({ index, target, onUpdate, onRemove }: AllowedTargetEditorProps) {
  function setPath(idx: number, path: string) {
    onUpdate({ ...target, paths: target.paths.map((p, i) => (i === idx ? path : p)) })
  }

  return (
    <div className={styles.patchCard}>
      <div className={styles.patchCardHeader}>
        <span className={styles.patchCardTitle}>Target {index + 1}</span>
        <IconButton tone="danger" onClick={onRemove} aria-label="Remove patch target">
          <CloseIcon />
        </IconButton>
      </div>

      <TargetFields
        target={target.target}
        onChange={(field, val) => onUpdate({ ...target, target: { ...target.target, [field]: val } })}
        namespacePlaceholder="any namespace"
      />

      <div>
        <label className={styles.label}>Allowed paths (JSONPath)</label>
        {target.paths.map((path, i) => (
          <div key={i} className={styles.setRow}>
            <input
              className={styles.setKey}
              value={path}
              onChange={(e) => setPath(i, e.target.value)}
              placeholder=".spec.replicas"
            />
            <IconButton
              tone="danger"
              onClick={() => onUpdate({ ...target, paths: target.paths.filter((_, j) => j !== i) })}
              aria-label="Remove path"
            >
              <CloseIcon />
            </IconButton>
          </div>
        ))}
        <Button
          variant="ghost"
          size="sm"
          icon={<PlusIcon />}
          onClick={() => onUpdate({ ...target, paths: [...target.paths, ''] })}
        >
          Add path
        </Button>
      </div>
    </div>
  )
}

// ── VersionPicker ─────────────────────────────────────────────────────────────

interface VersionPickerProps {
  value: string
  tags: string[]
  loading: boolean
  onChange: (value: string) => void
}

function VersionPicker({ value, tags, loading, onChange }: VersionPickerProps) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  // Set to true when the user focuses the input while a fetch is in progress,
  // so we can auto-open the dropdown once loading finishes.
  const pendingOpenRef = useRef(false)

  // Close dropdown when clicking outside.
  useEffect(() => {
    function handleMouseDown(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handleMouseDown)
    return () => document.removeEventListener('mousedown', handleMouseDown)
  }, [])

  // When loading finishes, open the dropdown if the user already focused the field.
  useEffect(() => {
    if (!loading && pendingOpenRef.current) {
      pendingOpenRef.current = false
      const id = setTimeout(() => setOpen(true), 0)
      return () => clearTimeout(id)
    }
  }, [loading])

  function handleSelect(tag: string) {
    onChange(tag)
    setOpen(false)
  }

  const filteredTags = value
    ? tags.filter((t) => t.toLowerCase().includes(value.toLowerCase()))
    : tags

  const showDropdown = open && (filteredTags.length > 0 || loading)

  return (
    <div ref={containerRef} className={styles.versionPicker}>
      <div className={styles.versionInputWrap}>
        {loading && (
          <div className={styles.versionLoadingOverlay} aria-hidden="true">
            <span className={styles.versionSpinner} />
            Fetching versions…
          </div>
        )}
        <input
          className={styles.input}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onFocus={() => {
            if (loading) {
              pendingOpenRef.current = true
            } else {
              setOpen(true)
            }
          }}
          placeholder="1.0.0"
          readOnly={loading}
        />
      </div>
      {showDropdown && (
        <ul className={styles.versionDropdown} role="listbox">
          {loading && filteredTags.length === 0 && (
            <li className={styles.versionDropdownItem} style={{ opacity: 0.6, cursor: 'default' }}>
              Loading…
            </li>
          )}
          {filteredTags.map((tag) => (
            <li
              key={tag}
              className={`${styles.versionDropdownItem} ${tag === value ? styles.versionDropdownItemActive : ''}`}
              role="option"
              aria-selected={tag === value}
              onMouseDown={(e) => { e.preventDefault(); handleSelect(tag) }}
            >
              {tag}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
