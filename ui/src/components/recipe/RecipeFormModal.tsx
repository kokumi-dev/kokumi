import { useState } from 'react'
import yaml from 'js-yaml'
import Modal from '../shared/Modal'
import Btn from '../shared/Btn'
import YamlEditor from '../shared/YamlEditor'
import type { Recipe, RecipeFormData, Patch } from '../../api/types'
import { emptyRecipeForm, recipeToFormData } from '../../api/types'
import styles from './RecipeFormModal.module.css'

interface Props {
  /** When provided the modal is in "edit" mode. */
  recipe?: Recipe
  onClose: () => void
  onSubmit: (data: RecipeFormData) => Promise<void>
}

// ── YAML serialisation helpers ────────────────────────────────────────────────

function formToYaml(data: RecipeFormData): string {
  const doc: Record<string, unknown> = {
    source: { oci: data.source.oci, version: data.source.version },
    destination: { oci: data.destination.oci },
    autoDeploy: data.autoDeploy,
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

function yamlToPartialForm(text: string): Omit<RecipeFormData, 'name' | 'namespace'> {
  const doc = yaml.load(text) as Record<string, unknown>
  if (!doc || typeof doc !== 'object') throw new Error('YAML must be a mapping')

  const src = doc.source as Record<string, string> | undefined
  const dst = doc.destination as Record<string, string> | undefined
  const rawPatches = Array.isArray(doc.patches) ? (doc.patches as unknown[]) : []

  return {
    source: { oci: src?.oci ?? '', version: src?.version ?? '' },
    destination: { oci: dst?.oci ?? '' },
    autoDeploy: Boolean(doc.autoDeploy),
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

export default function RecipeFormModal({ recipe, onClose, onSubmit }: Props) {
  const isEdit = !!recipe
  const [tab, setTab] = useState<'form' | 'yaml'>('form')
  const [formData, setFormData] = useState<RecipeFormData>(
    recipe ? recipeToFormData(recipe) : emptyRecipeForm(),
  )
  const [yamlText, setYamlText] = useState(() => formToYaml(formData))
  const [yamlError, setYamlError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

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
    setSaving(true)
    try {
      await onSubmit(data)
    } finally {
      setSaving(false)
    }
  }

  // ── Form field helpers ─────────────────────────────────────────────────────

  function setField<K extends keyof RecipeFormData>(key: K, val: RecipeFormData[K]) {
    setFormData((prev) => ({ ...prev, [key]: val }))
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

  const footer = (
    <>
      <Btn variant="secondary" onClick={onClose} disabled={saving}>
        Cancel
      </Btn>
      <Btn variant="primary" onClick={handleSubmit} disabled={saving}>
        {saving ? 'Saving…' : isEdit ? 'Save Changes' : 'Create Recipe'}
      </Btn>
    </>
  )

  return (
    <Modal
      title={isEdit ? `Edit Recipe — ${recipe.name}` : 'Add Recipe'}
      onClose={onClose}
      footer={footer}
    >
      {/* ── Tabs ── */}
      <div className={styles.tabs}>
        <button
          className={`${styles.tab} ${tab === 'form' ? styles.tabActive : ''}`}
          onClick={() => (tab === 'yaml' ? switchToForm() : undefined)}
        >
          Form
        </button>
        <button
          className={`${styles.tab} ${tab === 'yaml' ? styles.tabActive : ''}`}
          onClick={() => (tab === 'form' ? switchToYaml() : undefined)}
        >
          YAML
        </button>
      </div>

      <div className={styles.tabContent}>
        {tab === 'form' ? (
          <FormView
            formData={formData}
            isEdit={isEdit}
            onFieldChange={setField}
            onAddPatch={addPatch}
            onRemovePatch={removePatch}
            onUpdatePatch={updatePatch}
          />
        ) : (
          <YamlView
            yamlText={yamlText}
            yamlError={yamlError}
            onChange={(v) => { setYamlText(v); setYamlError(null) }}
          />
        )}
      </div>
    </Modal>
  )
}

// ── FormView ──────────────────────────────────────────────────────────────────

interface FormViewProps {
  formData: RecipeFormData
  isEdit: boolean
  onFieldChange: <K extends keyof RecipeFormData>(key: K, val: RecipeFormData[K]) => void
  onAddPatch: () => void
  onRemovePatch: (idx: number) => void
  onUpdatePatch: (idx: number, p: Patch) => void
}

function FormView({
  formData,
  isEdit,
  onFieldChange,
  onAddPatch,
  onRemovePatch,
  onUpdatePatch,
}: FormViewProps) {
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
            placeholder="my-recipe"
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

      {/* Source */}
      <div className={styles.fieldGroup}>
        <p className={styles.sectionTitle}>Source</p>
      </div>
      <div className={styles.row2}>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>OCI Registry</label>
          <input
            className={styles.input}
            value={formData.source.oci}
            onChange={(e) => onFieldChange('source', { ...formData.source, oci: e.target.value })}
            placeholder="oci://registry/repo"
          />
        </div>
        <div className={styles.fieldGroup}>
          <label className={styles.label}>Version</label>
          <input
            className={styles.input}
            value={formData.source.version}
            onChange={(e) => onFieldChange('source', { ...formData.source, version: e.target.value })}
            placeholder="1.0.0"
          />
        </div>
      </div>

      {/* Destination */}
      <div className={styles.fieldGroup}>
        <p className={styles.sectionTitle}>Destination</p>
        <input
          className={styles.input}
          value={formData.destination.oci}
          onChange={(e) => onFieldChange('destination', { oci: e.target.value })}
          placeholder="oci://registry/rendered-repo"
        />
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

      {/* Patches */}
      <div>
        <p className={styles.sectionTitle}>Patches</p>
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
          placeholder="inherit from Recipe namespace"
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
