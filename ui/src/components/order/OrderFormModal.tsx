import { useState, useEffect, useRef } from 'react'
import yaml from 'js-yaml'
import Modal from '../shared/Modal'
import Btn from '../shared/Btn'
import YamlEditor from '../shared/YamlEditor'
import CommitMessageModal from '../shared/CommitMessageModal'
import PreviewTab from './PreviewTab'
import DiffTab from './DiffTab'
import type { Order, OrderFormData, Patch, HelmRender, Menu } from '../../api/types'
import { emptyOrderForm, orderToFormData } from '../../api/types'
import { objectToYaml, yamlToValues } from '../../utils/yaml'
import { getDefaultRegistry, listOCITags } from '../../api/client'
import styles from './OrderFormModal.module.css'

interface Props {
  /** When provided the modal is in "edit" mode. */
  order?: Order
  /** When provided, pre-fill menuRef and hide source fields. */
  menuRef?: { name: string }
  /** Full menu object — used to display override policy constraints. */
  menu?: Menu
  /** Available menus for the selector dropdown (create mode). */
  menus?: Menu[]
  onClose: () => void
  onSubmit: (data: OrderFormData, commitMessage: string) => Promise<void>
}

// ── YAML serialisation helpers ────────────────────────────────────────────────

function formToYaml(data: OrderFormData): string {
  const doc: Record<string, unknown> = {
    autoDeploy: data.autoDeploy,
  }
  if (data.destination.oci) {
    doc.destination = { oci: data.destination.oci }
  }
  if (data.menuRef) {
    doc.menuRef = { name: data.menuRef.name }
  }
  if (data.source) {
    doc.source = { oci: data.source.oci, version: data.source.version }
  }
  if (data.render?.helm) {
    const h = data.render.helm
    const helmDoc: Record<string, unknown> = {}
    if (h.releaseName) helmDoc.releaseName = h.releaseName
    if (h.namespace) helmDoc.namespace = h.namespace
    if (h.includeCRDs) helmDoc.includeCRDs = true
    if (Object.keys(h.values).length > 0) helmDoc.values = h.values
    doc.render = { helm: helmDoc }
  }
  if (data.patches.length > 0) {
    doc.patches = data.patches.map((p) => ({
      target: {
        kind: p.target.kind,
        name: p.target.name,
        ...(p.target.namespace ? { namespace: p.target.namespace } : {}),
      },
      set: p.set,
    }))
  }
  return yaml.dump(doc, { lineWidth: 100 })
}

function yamlToPartialForm(text: string): Omit<OrderFormData, 'name' | 'namespace'> {
  const doc = yaml.load(text) as Record<string, unknown>
  if (!doc || typeof doc !== 'object') throw new Error('YAML must be a mapping')

  const src = doc.source as Record<string, string> | undefined
  const dst = doc.destination as Record<string, string> | undefined
  const rawMenuRef = doc.menuRef as Record<string, string> | undefined
  const rawPatches = Array.isArray(doc.patches) ? (doc.patches as unknown[]) : []

  const rawRender = doc.render as Record<string, unknown> | undefined
  let render: OrderFormData['render']
  if (rawRender?.helm) {
    const h = rawRender.helm as Record<string, unknown>
    render = {
      helm: {
        releaseName: (h.releaseName as string) ?? '',
        namespace: (h.namespace as string) ?? '',
        includeCRDs: Boolean(h.includeCRDs),
        values: h.values && typeof h.values === 'object' && !Array.isArray(h.values)
          ? (h.values as Record<string, unknown>)
          : {},
      },
    }
  }

  return {
    menuRef: rawMenuRef?.name ? { name: rawMenuRef.name } : undefined,
    source: src?.oci ? { oci: src.oci, version: src.version ?? '' } : undefined,
    destination: { oci: dst?.oci ?? '' },
    render,
    autoDeploy: Boolean(doc.autoDeploy),
    edits: [],
    patches: rawPatches.map((p) => {
      const patch = p as Record<string, unknown>
      const target = (patch.target ?? {}) as Record<string, string>
      const set = (patch.set ?? {}) as Record<string, string>
      return {
        target: {
          kind: target.kind ?? '',
          name: target.name ?? '',
          namespace: target.namespace,
        },
        set,
      } satisfies Patch
    }),
  }
}

// ── Main component ────────────────────────────────────────────────────────────

export default function OrderFormModal({ order, menuRef, menu, menus, onClose, onSubmit }: Props) {
  const isEdit = !!order
  const showDiffTab = isEdit && !!order?.activePreparation
  const [tab, setTab] = useState<'form' | 'yaml' | 'preview' | 'diff'>('form')
  const [selectedMenu, setSelectedMenu] = useState<Menu | null>(null)
  const [defaultRegistry, setDefaultRegistry] = useState('')
  const [formData, setFormData] = useState<OrderFormData>(() => {
    if (order) return orderToFormData(order)
    if (menuRef) {
      const base = { ...emptyOrderForm(), menuRef, source: undefined }
      if (menu?.render?.helm) {
        base.render = { helm: { releaseName: '', namespace: '', includeCRDs: false, values: {} } }
      }
      return base
    }
    return emptyOrderForm()
  })
  const [yamlText, setYamlText] = useState(() => formToYaml(formData))
  const [yamlError, setYamlError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [showCommitModal, setShowCommitModal] = useState(false)
  const [pendingFormData, setPendingFormData] = useState<OrderFormData | null>(null)

  const initialYamlRef = useRef(isEdit ? formToYaml(orderToFormData(order!)) : '')

  const effectiveMenu = menu ?? selectedMenu

  useEffect(() => {
    getDefaultRegistry()
      .then(({ baseURL }) => setDefaultRegistry(baseURL))
      .catch(() => {})
  }, [])

  function handleMenuSelect(menuName: string) {
    if (!menuName) {
      setSelectedMenu(null)
      setFormData((prev) => ({
        ...prev,
        menuRef: undefined,
        source: prev.source ?? { oci: '', version: '' },
      }))
      return
    }
    const m = menus?.find((x) => x.name === menuName)
    if (!m) return
    setSelectedMenu(m)
    setFormData((prev) => ({
      ...prev,
      menuRef: { name: m.name },
      source: undefined,
      render: m.render?.helm
        ? { helm: { releaseName: '', namespace: '', includeCRDs: false, values: {} } }
        : prev.render,
    }))
  }

  // ── Tab switching ──────────────────────────────────────────────────────────

  function switchToYaml() {
    setYamlText(formToYaml(formData))
    setYamlError(null)
    setTab('yaml')
  }

  function switchToForm() {
    try {
      const partial = yamlToPartialForm(yamlText)
      setFormData((prev) => ({ ...prev, ...partial }))
      setYamlError(null)
      setTab('form')
    } catch (e) {
      setYamlError(e instanceof Error ? e.message : String(e))
    }
  }

  // ── Submit ─────────────────────────────────────────────────────────────────

  async function handleSubmit() {
    let data = formData
    if (tab === 'yaml') {
      try {
        const partial = yamlToPartialForm(yamlText)
        data = { ...formData, ...partial }
      } catch (e) {
        setYamlError(e instanceof Error ? e.message : String(e))
        return
      }
    }
    setPendingFormData(data)
    setShowCommitModal(true)
  }

  async function handleCommit(commitMessage: string) {
    if (!pendingFormData) return
    setSaving(true)
    try {
      await onSubmit(pendingFormData, commitMessage)
    } finally {
      setSaving(false)
      setShowCommitModal(false)
      setPendingFormData(null)
    }
  }

  // ── Form field helpers ─────────────────────────────────────────────────────

  function setField<K extends keyof OrderFormData>(key: K, val: OrderFormData[K]) {
    setFormData((prev) => ({ ...prev, [key]: val }))
  }

  function enableHelm() {
    setFormData((prev) => ({
      ...prev,
      render: { helm: { releaseName: '', namespace: '', includeCRDs: false, values: {} } },
    }))
  }

  function disableHelm() {
    setFormData((prev) => ({ ...prev, render: undefined }))
  }

  function updateHelm(h: HelmRender) {
    setFormData((prev) => ({ ...prev, render: { helm: h } }))
  }

  function addPatch() {
    setFormData((prev) => ({
      ...prev,
      patches: [...prev.patches, { target: { kind: '', name: '' }, set: {} }],
    }))
  }

  function removePatch(idx: number) {
    setFormData((prev) => ({
      ...prev,
      patches: prev.patches.filter((_, i) => i !== idx),
    }))
  }

  function updatePatch(idx: number, patch: Patch) {
    setFormData((prev) => {
      const patches = [...prev.patches]
      patches[idx] = patch
      return { ...prev, patches }
    })
  }

  // ── Render ─────────────────────────────────────────────────────────────────

  let isDirty = true
  if (isEdit) {
    if (tab === 'form' || tab === 'preview' || tab === 'diff') {
      isDirty = formToYaml(formData) !== initialYamlRef.current
    } else {
      try {
        const partial = yamlToPartialForm(yamlText)
        isDirty = formToYaml({ ...formData, ...partial }) !== initialYamlRef.current
      } catch {
        isDirty = true
      }
    }
  }

  const footer = (
    <>
      <Btn variant="secondary" onClick={onClose} disabled={saving}>
        Cancel
      </Btn>
      <Btn variant="primary" onClick={handleSubmit} disabled={saving || !isDirty}>
        {saving ? 'Saving…' : isEdit ? 'Save Changes' : 'Create Order'}
      </Btn>
    </>
  )

  return (
    <Modal
      title={isEdit ? `Edit Order — ${order.name}` : 'Add Order'}
      onClose={onClose}
      footer={footer}
    >
      {showCommitModal && (
        <CommitMessageModal
          onClose={() => { setShowCommitModal(false); setPendingFormData(null) }}
          onCommit={handleCommit}
          loading={saving}
        />
      )}
      {/* ── Tabs ── */}
      <div className={styles.tabs}>
        <button
          className={`${styles.tab} ${tab === 'form' ? styles.tabActive : ''}`}
          onClick={() => {
            if (tab === 'yaml') switchToForm()
            else if (tab === 'preview' || tab === 'diff') setTab('form')
          }}
        >
          Form
        </button>
        <button
          className={`${styles.tab} ${tab === 'yaml' ? styles.tabActive : ''}`}
          onClick={() => {
            if (tab === 'form') switchToYaml()
            else if (tab === 'preview' || tab === 'diff') setTab('yaml')
          }}
        >
          YAML
        </button>
        <button
          className={`${styles.tab} ${tab === 'preview' ? styles.tabActive : ''}`}
          onClick={() => {
            if (tab === 'yaml') {
              try {
                const partial = yamlToPartialForm(yamlText)
                setFormData((prev) => ({ ...prev, ...partial }))
                setYamlError(null)
              } catch {
                // keep current formData
              }
            }
            setTab('preview')
          }}
        >
          Preview
        </button>
        {showDiffTab && (
          <button
            className={`${styles.tab} ${tab === 'diff' ? styles.tabActive : ''}`}
            onClick={() => {
              if (tab === 'yaml') {
                try {
                  const partial = yamlToPartialForm(yamlText)
                  setFormData((prev) => ({ ...prev, ...partial }))
                  setYamlError(null)
                } catch {
                  // keep current formData
                }
              }
              setTab('diff')
            }}
          >
            Diff
          </button>
        )}
      </div>

      <div className={styles.tabContent}>
        {tab === 'form' && (
          <FormView
            formData={formData}
            isEdit={isEdit}
            defaultRegistry={defaultRegistry}
            menu={effectiveMenu ?? undefined}
            menus={menus}
            hasPresetMenu={!!menuRef || !!menu}
            onMenuSelect={handleMenuSelect}
            onFieldChange={setField}
            onEnableHelm={enableHelm}
            onDisableHelm={disableHelm}
            onUpdateHelm={updateHelm}
            onAddPatch={addPatch}
            onRemovePatch={removePatch}
            onUpdatePatch={updatePatch}
          />
        )}
        {tab === 'yaml' && (
          <YamlView
            yamlText={yamlText}
            yamlError={yamlError}
            onChange={(v) => { setYamlText(v); setYamlError(null) }}
          />
        )}
        {tab === 'preview' && (
          <PreviewTab formData={formData} />
        )}
        {tab === 'diff' && showDiffTab && (
          <DiffTab formData={formData} order={order!} />
        )}
      </div>
    </Modal>
  )
}

// ── FormView ──────────────────────────────────────────────────────────────────

interface FormViewProps {
  formData: OrderFormData
  isEdit: boolean
  defaultRegistry: string
  menu?: Menu
  menus?: Menu[]
  hasPresetMenu: boolean
  onMenuSelect: (menuName: string) => void
  onFieldChange: <K extends keyof OrderFormData>(key: K, val: OrderFormData[K]) => void
  onEnableHelm: () => void
  onDisableHelm: () => void
  onUpdateHelm: (h: HelmRender) => void
  onAddPatch: () => void
  onRemovePatch: (idx: number) => void
  onUpdatePatch: (idx: number, p: Patch) => void
}

function FormView({
  formData,
  isEdit,
  defaultRegistry,
  menu,
  menus,
  hasPresetMenu,
  onMenuSelect,
  onFieldChange,
  onEnableHelm,
  onDisableHelm,
  onUpdateHelm,
  onAddPatch,
  onRemovePatch,
  onUpdatePatch,
}: FormViewProps) {
  const valuesPolicy = menu?.overrides.values.policy
  const patchesPolicy = menu?.overrides.patches.policy

  const [versionTags, setVersionTags] = useState<string[]>([])
  const [versionTagsLoading, setVersionTagsLoading] = useState(false)
  const lastFetchedRef = useRef<string>('')
  const fetchSeqRef = useRef(0)

  function handleOciBlur() {
    const oci = formData.source?.oci ?? ''
    if (!oci || oci === lastFetchedRef.current) return
    lastFetchedRef.current = oci
    const seq = ++fetchSeqRef.current
    setVersionTagsLoading(true)
    listOCITags(oci)
      .then((tags) => { if (fetchSeqRef.current === seq) setVersionTags(tags) })
      .catch(() => { /* non-blocking: keep previous tags */ })
      .finally(() => { if (fetchSeqRef.current === seq) setVersionTagsLoading(false) })
  }

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
            placeholder="my-order"
          />
        </div>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Namespace</label>
          <input
            className={`${styles.input} ${isEdit ? styles.inputDisabled : ''}`}
            value={formData.namespace}
            onChange={(e) => onFieldChange('namespace', e.target.value)}
            readOnly={isEdit}
            placeholder="default"
          />
        </div>
      </div>

      {/* Menu selector (create mode, when menus are available and no preset menu) */}
      {!isEdit && !hasPresetMenu && menus && menus.length > 0 && (
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Source Type</label>
          <select
            className={styles.input}
            value={formData.menuRef?.name ?? ''}
            onChange={(e) => onMenuSelect(e.target.value)}
          >
            <option value="">Standalone (manual source)</option>
            {menus.map((m) => (
              <option key={m.name} value={m.name}>Menu: {m.name}</option>
            ))}
          </select>
        </div>
      )}

      {/* Source or Menu Reference */}
      {formData.menuRef ? (
        <div className={styles.fieldGroup}>
          <p className={styles.sectionTitle}>Menu Reference</p>
          <input
            className={`${styles.input} ${styles.inputDisabled}`}
            value={formData.menuRef.name}
            readOnly
          />
          <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted-light)', marginTop: 4 }}>
            Source and version are provided by the Menu
          </span>
        </div>
      ) : (
        <>
          <div className={styles.fieldGroup}>
            <p className={styles.sectionTitle}>Source</p>
          </div>
          <div className={styles.row2}>
            <div className={styles.fieldGroup}>
              <label className={styles.label}>OCI Registry</label>
              <input
                className={styles.input}
                value={formData.source?.oci ?? ''}
                onChange={(e) => onFieldChange('source', { ...(formData.source ?? { oci: '', version: '' }), oci: e.target.value })}
                onBlur={handleOciBlur}
                placeholder="oci://registry/repo"
              />
            </div>
            <div className={styles.fieldGroup}>
              <label className={styles.label}>Version</label>
              <VersionPicker
                value={formData.source?.version ?? ''}
                tags={versionTags}
                loading={versionTagsLoading}
                onChange={(v) => onFieldChange('source', { ...(formData.source ?? { oci: '', version: '' }), version: v })}
              />
            </div>
          </div>
        </>
      )}

      {/* Destination */}
      <div className={styles.fieldGroup}>
        <label className={styles.label}>Destination OCI <span style={{ fontWeight: 400, textTransform: 'none', letterSpacing: 0 }}>(optional)</span></label>
        <input
          className={styles.input}
          value={formData.destination.oci}
          onChange={(e) => onFieldChange('destination', { oci: e.target.value })}
          placeholder={
            defaultRegistry
              ? `oci://${defaultRegistry}/${formData.namespace || 'namespace'}/${formData.name || 'name'}`
              : 'oci://registry/rendered-repo'
          }
        />
        {defaultRegistry && !formData.destination.oci && (
          <span style={{ fontSize: '0.75rem', color: 'var(--color-text-muted-light)', marginTop: 2 }}>
            Leave blank to use the in-cluster registry automatically
          </span>
        )}
      </div>

      {/* AutoDeploy */}
      <label className={styles.checkRow}>
        <input
          type="checkbox"
          checked={formData.autoDeploy}
          onChange={(e) => onFieldChange('autoDeploy', e.target.checked)}
        />
        Auto Deploy — automatically promote newly created Preparations
      </label>

      {/* Renderer */}
      <div>
        <p className={styles.sectionTitle}>Renderer</p>
        {valuesPolicy === 'None' ? (
          <div className={styles.policyBanner}>
            <span className={styles.policyIcon}>🔒</span>
            Value overrides are locked by the Menu
          </div>
        ) : (
          <>
            {!menu && (
              <label className={styles.checkRow}>
                <input
                  type="checkbox"
                  checked={!!formData.render?.helm}
                  onChange={(e) => (e.target.checked ? onEnableHelm() : onDisableHelm())}
                />
                Enable Helm rendering
              </label>
            )}
            {valuesPolicy === 'Restricted' && menu?.overrides.values.allowed && (
              <div className={styles.policyBanner}>
                <span className={styles.policyIcon}>📋</span>
                Allowed values:{' '}
                {menu.overrides.values.allowed.map((k) => (
                  <span key={k} className={styles.policyChip}>{k}</span>
                ))}
              </div>
            )}
            {valuesPolicy === 'All' && menu && (
              <div className={styles.policyBannerOpen}>
                <span className={styles.policyIcon}>✓</span>
                All value overrides are allowed
              </div>
            )}
            {formData.render?.helm && (
              <div className={styles.helmSection}>
                <HelmRenderEditor helm={formData.render.helm} onUpdate={onUpdateHelm} />
              </div>
            )}
          </>
        )}
      </div>

      {/* Patches */}
      <div>
        <p className={styles.sectionTitle}>Patches</p>
        {patchesPolicy === 'None' ? (
          <div className={styles.policyBanner}>
            <span className={styles.policyIcon}>🔒</span>
            Patch overrides are locked by the Menu
          </div>
        ) : (
          <>
            {patchesPolicy === 'Restricted' && menu?.overrides.patches.allowed && (
              <div className={styles.policyBanner}>
                <span className={styles.policyIcon}>📋</span>
                Allowed patches:{' '}
                {menu.overrides.patches.allowed.map((a, i) => (
                  <span key={i} className={styles.policyChip}>
                    {a.target.kind}/{a.target.name}: {a.paths.join(', ')}
                  </span>
                ))}
              </div>
            )}
            {patchesPolicy === 'All' && menu && (
              <div className={styles.policyBannerOpen}>
                <span className={styles.policyIcon}>✓</span>
                All patch overrides are allowed
              </div>
            )}
            <div className={styles.patchList}>
              {formData.patches.map((patch, idx) => (
                <PatchEditor
                  key={idx}
                  index={idx}
                  patch={patch}
                  onUpdate={(p) => onUpdatePatch(idx, p)}
                  onRemove={() => onRemovePatch(idx)}
                />
              ))}
            </div>
            <button className={styles.addPatchBtn} onClick={onAddPatch}>
              + Add Patch
            </button>
          </>
        )}
      </div>
    </div>
  )
}

// ── PatchEditor ───────────────────────────────────────────────────────────────

interface PatchEditorProps {
  index: number
  patch: Patch
  onUpdate: (p: Patch) => void
  onRemove: () => void
}

function PatchEditor({ index, patch, onUpdate, onRemove }: PatchEditorProps) {
  const setEntries = Object.entries(patch.set)

  function updateTarget(field: keyof Patch['target'], val: string) {
    onUpdate({ ...patch, target: { ...patch.target, [field]: val } })
  }

  function addSetEntry() {
    onUpdate({ ...patch, set: { ...patch.set, '': '' } })
  }

  function updateSetEntry(oldKey: string, newKey: string, val: string) {
    const next: Record<string, string> = {}
    for (const [k, v] of Object.entries(patch.set)) {
      if (k === oldKey) {
        next[newKey] = val
      } else {
        next[k] = v
      }
    }
    onUpdate({ ...patch, set: next })
  }

  function removeSetEntry(key: string) {
    const next = { ...patch.set }
    delete next[key]
    onUpdate({ ...patch, set: next })
  }

  return (
    <div className={styles.patchCard}>
      <div className={styles.patchCardHeader}>
        <span className={styles.patchCardTitle}>Patch {index + 1}</span>
        <button className={styles.iconBtn} onClick={onRemove} title="Remove patch">
          <svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <path d="M2 2l8 8M10 2L2 10" />
          </svg>
        </button>
      </div>

      <div className={styles.row2}>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Kind</label>
          <input
            className={styles.input}
            value={patch.target.kind}
            onChange={(e) => updateTarget('kind', e.target.value)}
            placeholder="Deployment"
          />
        </div>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Name</label>
          <input
            className={styles.input}
            value={patch.target.name}
            onChange={(e) => updateTarget('name', e.target.value)}
            placeholder="my-app"
          />
        </div>
      </div>

      <div className={styles.fieldGroup}>
        <label className={styles.label}>Namespace (optional)</label>
        <input
          className={styles.input}
          value={patch.target.namespace ?? ''}
          onChange={(e) => updateTarget('namespace', e.target.value)}
          placeholder="inherit from Order namespace"
        />
      </div>

      <div>
        <label className={styles.label}>Set (JSONPath → value)</label>
        {setEntries.map(([k, v], i) => (
          <div key={i} className={styles.setRow}>
            <input
              className={styles.setKey}
              value={k}
              onChange={(e) => updateSetEntry(k, e.target.value, v)}
              placeholder=".spec.replicas"
            />
            <input
              className={styles.setValue}
              value={v}
              onChange={(e) => updateSetEntry(k, k, e.target.value)}
              placeholder="3"
            />
            <button
              className={styles.iconBtn}
              onClick={() => removeSetEntry(k)}
              title="Remove"
            >
              <svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                <path d="M2 2l8 8M10 2L2 10" />
              </svg>
            </button>
          </div>
        ))}
        <button className={styles.addSetBtn} onClick={addSetEntry}>
          + Add key/value
        </button>
      </div>
    </div>
  )
}

// ── HelmRenderEditor ──────────────────────────────────────────────────────────

interface HelmRenderEditorProps {
  helm: HelmRender
  onUpdate: (h: HelmRender) => void
}

function HelmRenderEditor({ helm, onUpdate }: HelmRenderEditorProps) {
  const [valuesYaml, setValuesYaml] = useState(() => objectToYaml(helm.values))
  const [valuesError, setValuesError] = useState<string | null>(null)

  function handleValuesChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    const text = e.target.value
    setValuesYaml(text)
    try {
      const values = yamlToValues(text)
      setValuesError(null)
      onUpdate({ ...helm, values })
    } catch (err) {
      setValuesError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className={styles.helmCard}>
      <div className={styles.row2}>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Release Name</label>
          <input
            className={styles.input}
            value={helm.releaseName}
            onChange={(e) => onUpdate({ ...helm, releaseName: e.target.value })}
            placeholder="defaults to Order name"
          />
        </div>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Namespace</label>
          <input
            className={styles.input}
            value={helm.namespace}
            onChange={(e) => onUpdate({ ...helm, namespace: e.target.value })}
            placeholder="defaults to Order namespace"
          />
        </div>
      </div>

      <label className={styles.checkRow}>
        <input
          type="checkbox"
          checked={helm.includeCRDs}
          onChange={(e) => onUpdate({ ...helm, includeCRDs: e.target.checked })}
        />
        Include CRDs
      </label>

      <div className={styles.fieldGroup}>
        <label className={styles.label}>Values (YAML)</label>
        <textarea
          className={styles.valuesArea}
          value={valuesYaml}
          onChange={handleValuesChange}
          placeholder={'replicaCount: 2\nimage:\n  tag: v1.0.0'}
          spellCheck={false}
        />
        {valuesError && <p className={styles.valuesError}>{valuesError}</p>}
      </div>
    </div>
  )
}

// ── YamlView ──────────────────────────────────────────────────────────────────

interface YamlViewProps {
  yamlText: string
  yamlError: string | null
  onChange: (v: string) => void
}

function YamlView({ yamlText, yamlError, onChange }: YamlViewProps) {
  return (
    <>
      <YamlEditor value={yamlText} onChange={onChange} />
      {yamlError && <p className={styles.yamlError}>Parse error: {yamlError}</p>}
    </>
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
