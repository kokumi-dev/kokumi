import { useState } from 'react'
import yaml from 'js-yaml'
import Modal from '../shared/Modal'
import Btn from '../shared/Btn'
import YamlEditor from '../shared/YamlEditor'
import type { Menu, MenuFormData, Patch, HelmRender, OverridePolicy } from '../../api/types'
import { emptyMenuForm, menuToFormData } from '../../api/types'
import { objectToYaml, yamlToValues } from '../../utils/yaml'
import formStyles from './MenuFormModal.module.css'

interface Props {
  menu?: Menu
  onClose: () => void
  onSubmit: (data: MenuFormData) => Promise<void>
}

function formToYaml(data: MenuFormData): string {
  const doc: Record<string, unknown> = {
    source: { oci: data.source.oci, version: data.source.version },
    overrides: data.overrides,
    defaults: data.defaults,
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

function yamlToPartialForm(text: string): Omit<MenuFormData, 'name'> {
  const doc = yaml.load(text) as Record<string, unknown>
  if (!doc || typeof doc !== 'object') throw new Error('YAML must be a mapping')

  const src = doc.source as Record<string, string> | undefined
  const rawPatches = Array.isArray(doc.patches) ? (doc.patches as unknown[]) : []
  const rawOverrides = doc.overrides as OverridePolicy | undefined
  const rawDefaults = doc.defaults as Record<string, unknown> | undefined

  const rawRender = doc.render as Record<string, unknown> | undefined
  let render: MenuFormData['render']
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
    source: { oci: src?.oci ?? '', version: src?.version ?? '' },
    render,
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
    overrides: rawOverrides ?? { values: { policy: 'None' }, patches: { policy: 'None' } },
    defaults: { autoDeploy: rawDefaults?.autoDeploy === 'Enabled' ? 'Enabled' : 'Disabled' },
  }
}

export default function MenuFormModal({ menu, onClose, onSubmit }: Props) {
  const isEdit = !!menu
  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [formData, setFormData] = useState<MenuFormData>(
    menu ? menuToFormData(menu) : emptyMenuForm(),
  )
  const [yamlText, setYamlText] = useState(() => formToYaml(formData))
  const [yamlError, setYamlError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

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
    setSaving(true)
    try {
      await onSubmit(data)
    } finally {
      setSaving(false)
    }
  }

  function setField<K extends keyof MenuFormData>(key: K, val: MenuFormData[K]) {
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

  const footer = (
    <>
      <Btn variant="secondary" onClick={onClose} disabled={saving}>Cancel</Btn>
      <Btn variant="primary" onClick={handleSubmit} disabled={saving}>
        {saving ? 'Saving…' : isEdit ? 'Save Changes' : 'Create Menu'}
      </Btn>
    </>
  )

  return (
    <Modal
      title={isEdit ? `Edit Menu — ${menu.name}` : 'Add Menu'}
      onClose={onClose}
      footer={footer}
    >
      <div className={formStyles.tabs}>
        <button
          className={`${formStyles.tab} ${tab === 'form' ? formStyles.tabActive : ''}`}
          onClick={() => (tab === 'yaml' ? switchToForm() : undefined)}
        >
          Form
        </button>
        <button
          className={`${formStyles.tab} ${tab === 'yaml' ? formStyles.tabActive : ''}`}
          onClick={() => (tab === 'form' ? switchToYaml() : undefined)}
        >
          YAML
        </button>
      </div>

      <div className={formStyles.tabContent}>
        {tab === 'form' ? (
          <MenuFormView
            formData={formData}
            isEdit={isEdit}
            onFieldChange={setField}
            onEnableHelm={enableHelm}
            onDisableHelm={disableHelm}
            onUpdateHelm={updateHelm}
            onAddPatch={addPatch}
            onRemovePatch={removePatch}
            onUpdatePatch={updatePatch}
          />
        ) : (
          <>
            <YamlEditor value={yamlText} onChange={(v) => { setYamlText(v); setYamlError(null) }} />
            {yamlError && <p className={formStyles.yamlError}>Parse error: {yamlError}</p>}
          </>
        )}
      </div>
    </Modal>
  )
}

// ── MenuFormView ──────────────────────────────────────────────────────────────

interface MenuFormViewProps {
  formData: MenuFormData
  isEdit: boolean
  onFieldChange: <K extends keyof MenuFormData>(key: K, val: MenuFormData[K]) => void
  onEnableHelm: () => void
  onDisableHelm: () => void
  onUpdateHelm: (h: HelmRender) => void
  onAddPatch: () => void
  onRemovePatch: (idx: number) => void
  onUpdatePatch: (idx: number, p: Patch) => void
}

function MenuFormView({
  formData,
  isEdit,
  onFieldChange,
  onEnableHelm,
  onDisableHelm,
  onUpdateHelm,
  onAddPatch,
  onRemovePatch,
  onUpdatePatch,
}: MenuFormViewProps) {
  return (
    <div className={formStyles.formGrid}>
      {/* Name */}
      <div className={formStyles.fieldGroup}>
        <label className={formStyles.label}>Name</label>
        <input
          className={`${formStyles.input} ${isEdit ? formStyles.inputDisabled : ''}`}
          value={formData.name}
          onChange={(e) => onFieldChange('name', e.target.value)}
          readOnly={isEdit}
          placeholder="my-menu"
        />
      </div>

      {/* Source */}
      <div className={formStyles.fieldGroup}>
        <p className={formStyles.sectionTitle}>Source</p>
      </div>
      <div className={formStyles.row2}>
        <div className={formStyles.fieldGroup}>
          <label className={formStyles.label}>OCI Registry</label>
          <input
            className={formStyles.input}
            value={formData.source.oci}
            onChange={(e) => onFieldChange('source', { ...formData.source, oci: e.target.value })}
            placeholder="oci://registry/repo"
          />
        </div>
        <div className={formStyles.fieldGroup}>
          <label className={formStyles.label}>Version</label>
          <input
            className={formStyles.input}
            value={formData.source.version}
            onChange={(e) => onFieldChange('source', { ...formData.source, version: e.target.value })}
            placeholder="1.0.0"
          />
        </div>
      </div>

      {/* Defaults */}
      <label className={formStyles.checkRow}>
        <input
          type="checkbox"
          checked={formData.defaults.autoDeploy === 'Enabled'}
          onChange={(e) => onFieldChange('defaults', { ...formData.defaults, autoDeploy: e.target.checked ? 'Enabled' : 'Disabled' })}
        />
        Default Auto Deploy — Orders using this Menu inherit auto-deploy
      </label>

      {/* Override Policies */}
      <div>
        <p className={formStyles.sectionTitle}>Override Policies</p>
        <OverridePolicyEditor
          overrides={formData.overrides}
          onChange={(o) => onFieldChange('overrides', o)}
        />
      </div>

      {/* Renderer */}
      <div>
        <p className={formStyles.sectionTitle}>Base Renderer</p>
        <label className={formStyles.checkRow}>
          <input
            type="checkbox"
            checked={!!formData.render?.helm}
            onChange={(e) => (e.target.checked ? onEnableHelm() : onDisableHelm())}
          />
          Enable Helm rendering
        </label>
        {formData.render?.helm && (
          <div className={formStyles.helmSection}>
            <HelmEditor helm={formData.render.helm} onUpdate={onUpdateHelm} />
          </div>
        )}
      </div>

      {/* Base Patches */}
      <div>
        <p className={formStyles.sectionTitle}>Base Patches</p>
        <div className={formStyles.patchList}>
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
        <button className={formStyles.addPatchBtn} onClick={onAddPatch}>
          + Add Patch
        </button>
      </div>
    </div>
  )
}

// ── OverridePolicyEditor ──────────────────────────────────────────────────────

interface OverridePolicyEditorProps {
  overrides: OverridePolicy
  onChange: (o: OverridePolicy) => void
}

function OverridePolicyEditor({ overrides, onChange }: OverridePolicyEditorProps) {
  const policyOptions = ['All', 'Restricted', 'None'] as const

  function setValuesPolicy(policy: 'All' | 'Restricted' | 'None') {
    onChange({
      ...overrides,
      values: {
        policy,
        allowed: policy === 'Restricted' ? (overrides.values.allowed ?? []) : undefined,
      },
    })
  }

  function setPatchesPolicy(policy: 'All' | 'Restricted' | 'None') {
    onChange({
      ...overrides,
      patches: {
        policy,
        allowed: policy === 'Restricted' ? (overrides.patches.allowed ?? []) : undefined,
      },
    })
  }

  return (
    <div className={formStyles.formGrid}>
      <div className={formStyles.row2}>
        <div className={formStyles.fieldGroup}>
          <label className={formStyles.label}>Values Override Policy</label>
          <select
            className={formStyles.input}
            value={overrides.values.policy}
            onChange={(e) => setValuesPolicy(e.target.value as 'All' | 'Restricted' | 'None')}
          >
            {policyOptions.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
        </div>
        <div className={formStyles.fieldGroup}>
          <label className={formStyles.label}>Patches Override Policy</label>
          <select
            className={formStyles.input}
            value={overrides.patches.policy}
            onChange={(e) => setPatchesPolicy(e.target.value as 'All' | 'Restricted' | 'None')}
          >
            {policyOptions.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
        </div>
      </div>

      {overrides.values.policy === 'Restricted' && (
        <div className={formStyles.fieldGroup}>
          <label className={formStyles.label}>Allowed Value Paths (comma-separated)</label>
          <input
            className={formStyles.input}
            value={(overrides.values.allowed ?? []).join(', ')}
            onChange={(e) =>
              onChange({
                ...overrides,
                values: {
                  ...overrides.values,
                  allowed: e.target.value.split(',').map((s) => s.trim()).filter(Boolean),
                },
              })
            }
            placeholder="ui.message, replicaCount"
          />
        </div>
      )}
    </div>
  )
}

// ── PatchEditor (reused pattern from OrderFormModal) ──────────────────────────

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
      if (k === oldKey) next[newKey] = val
      else next[k] = v
    }
    onUpdate({ ...patch, set: next })
  }

  function removeSetEntry(key: string) {
    const next = { ...patch.set }
    delete next[key]
    onUpdate({ ...patch, set: next })
  }

  return (
    <div className={formStyles.patchCard}>
      <div className={formStyles.patchCardHeader}>
        <span className={formStyles.patchCardTitle}>Patch {index + 1}</span>
        <button className={formStyles.iconBtn} onClick={onRemove} title="Remove patch">
          <svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <path d="M2 2l8 8M10 2L2 10" />
          </svg>
        </button>
      </div>

      <div className={formStyles.row2}>
        <div className={formStyles.fieldGroup}>
          <label className={formStyles.label}>Kind</label>
          <input
            className={formStyles.input}
            value={patch.target.kind}
            onChange={(e) => updateTarget('kind', e.target.value)}
            placeholder="Deployment"
          />
        </div>
        <div className={formStyles.fieldGroup}>
          <label className={formStyles.label}>Name</label>
          <input
            className={formStyles.input}
            value={patch.target.name}
            onChange={(e) => updateTarget('name', e.target.value)}
            placeholder="my-app"
          />
        </div>
      </div>

      <div>
        <label className={formStyles.label}>Set (JSONPath → value)</label>
        {setEntries.map(([k, v], i) => (
          <div key={i} className={formStyles.setRow}>
            <input
              className={formStyles.setKey}
              value={k}
              onChange={(e) => updateSetEntry(k, e.target.value, v)}
              placeholder=".spec.replicas"
            />
            <input
              className={formStyles.setValue}
              value={v}
              onChange={(e) => updateSetEntry(k, k, e.target.value)}
              placeholder="3"
            />
            <button className={formStyles.iconBtn} onClick={() => removeSetEntry(k)} title="Remove">
              <svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                <path d="M2 2l8 8M10 2L2 10" />
              </svg>
            </button>
          </div>
        ))}
        <button className={formStyles.addSetBtn} onClick={addSetEntry}>
          + Add key/value
        </button>
      </div>
    </div>
  )
}

// ── HelmEditor ────────────────────────────────────────────────────────────────

interface HelmEditorProps {
  helm: HelmRender
  onUpdate: (h: HelmRender) => void
}

function HelmEditor({ helm, onUpdate }: HelmEditorProps) {
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
    <div className={formStyles.helmCard}>
      <div className={formStyles.row2}>
        <div className={formStyles.fieldGroup}>
          <label className={formStyles.label}>Release Name</label>
          <input
            className={formStyles.input}
            value={helm.releaseName}
            onChange={(e) => onUpdate({ ...helm, releaseName: e.target.value })}
            placeholder="defaults to Order name"
          />
        </div>
        <div className={formStyles.fieldGroup}>
          <label className={formStyles.label}>Namespace</label>
          <input
            className={formStyles.input}
            value={helm.namespace}
            onChange={(e) => onUpdate({ ...helm, namespace: e.target.value })}
            placeholder="defaults to Order namespace"
          />
        </div>
      </div>

      <label className={formStyles.checkRow}>
        <input
          type="checkbox"
          checked={helm.includeCRDs}
          onChange={(e) => onUpdate({ ...helm, includeCRDs: e.target.checked })}
        />
        Include CRDs
      </label>

      <div className={formStyles.fieldGroup}>
        <label className={formStyles.label}>Values (YAML)</label>
        <textarea
          className={formStyles.valuesArea}
          value={valuesYaml}
          onChange={handleValuesChange}
          placeholder={'replicaCount: 2\nimage:\n  tag: v1.0.0'}
          spellCheck={false}
        />
        {valuesError && <p className={formStyles.valuesError}>{valuesError}</p>}
      </div>
    </div>
  )
}
