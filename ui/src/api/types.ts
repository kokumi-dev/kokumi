// ── Mirror of Go server DTOs ──────────────────────────────────────────────────

export interface OCISource {
  oci: string
  version: string
}

export interface OCIDestination {
  oci: string
}

export interface PatchTarget {
  kind: string
  name: string
  namespace?: string
}

export interface Patch {
  target: PatchTarget
  set: Record<string, string>
}

export interface HelmRender {
  releaseName: string
  namespace: string
  includeCRDs: boolean
  values: Record<string, unknown>
}

export interface Render {
  helm?: HelmRender
}

export interface Condition {
  type: string
  status: string
  reason?: string
  message?: string
  lastTransitionTime?: string
}

export interface MenuRef {
  name: string
}

export interface Order {
  name: string
  namespace: string
  labels?: Record<string, string>
  source?: OCISource
  menuRef?: MenuRef
  destination: OCIDestination
  effectiveDestination?: string
  render?: Render
  patches?: Patch[]
  edits?: Patch[]
  autoDeploy: boolean
  phase: string
  latestRevision?: string
  activePreparation?: string
  conditions?: Condition[]
  createdAt?: string
}

export interface Artifact {
  ociRef: string
  digest: string
  signed: boolean
}

export interface Preparation {
  name: string
  namespace: string
  order: string
  artifact: Artifact
  configHash: string
  phase: string
  createdAt?: string
  isActive: boolean
  commitMessage?: string
  parentDigest?: string
  conditions?: Condition[]
}

export interface Serving {
  name: string
  namespace: string
  order: string
  desiredPreparation: string
  observedPreparation?: string
  deployedDigest?: string
  preparationPolicy: string
  phase: string
  conditions?: Condition[]
  createdAt?: string
}

// ── Menu types ────────────────────────────────────────────────────────────────

export interface ValueOverridePolicy {
  policy: 'All' | 'Restricted' | 'None'
  allowed?: string[]
}

export interface AllowedPatchTarget {
  target: PatchTarget
  paths: string[]
}

export interface PatchOverridePolicy {
  policy: 'All' | 'Restricted' | 'None'
  allowed?: AllowedPatchTarget[]
}

export interface OverridePolicy {
  values: ValueOverridePolicy
  patches: PatchOverridePolicy
}

export interface MenuDefaults {
  autoDeploy: boolean
}

export interface Menu {
  name: string
  source: OCISource
  render?: Render
  patches?: Patch[]
  overrides: OverridePolicy
  defaults: MenuDefaults
  phase?: string
  conditions?: Condition[]
  createdAt?: string
}

// ── Form data types ───────────────────────────────────────────────────────────

export interface OrderFormData {
  name: string
  namespace: string
  menuRef?: MenuRef
  source?: OCISource
  destination: OCIDestination
  render?: Render
  patches: Patch[]
  edits: Patch[]
  autoDeploy: boolean
}

export const emptyOrderForm = (): OrderFormData => ({
  name: '',
  namespace: 'default',
  source: { oci: '', version: '' },
  destination: { oci: '' },
  render: undefined,
  patches: [],
  edits: [],
  autoDeploy: false,
})

export const orderToFormData = (r: Order): OrderFormData => ({
  name: r.name,
  namespace: r.namespace,
  menuRef: r.menuRef,
  source: r.source ? { ...r.source } : undefined,
  destination: r.destination ? { ...r.destination } : { oci: '' },
  render: r.render?.helm
    ? {
        helm: {
          releaseName: r.render.helm.releaseName ?? '',
          namespace: r.render.helm.namespace ?? '',
          includeCRDs: r.render.helm.includeCRDs ?? false,
          values: r.render.helm.values ?? {},
        },
      }
    : undefined,
  patches: (r.patches ?? []).map((p) => ({
    target: { ...p.target },
    set: { ...p.set },
  })),
  edits: (r.edits ?? []).map((p) => ({
    target: { ...p.target },
    set: { ...p.set },
  })),
  autoDeploy: r.autoDeploy,
})

export interface MenuFormData {
  name: string
  source: OCISource
  render?: Render
  patches: Patch[]
  overrides: OverridePolicy
  defaults: MenuDefaults
}

export const emptyMenuForm = (): MenuFormData => ({
  name: '',
  source: { oci: '', version: '' },
  render: undefined,
  patches: [],
  overrides: {
    values: { policy: 'None' },
    patches: { policy: 'None' },
  },
  defaults: { autoDeploy: false },
})

export const menuToFormData = (m: Menu): MenuFormData => ({
  name: m.name,
  source: { ...m.source },
  render: m.render?.helm
    ? {
        helm: {
          releaseName: m.render.helm.releaseName ?? '',
          namespace: m.render.helm.namespace ?? '',
          includeCRDs: m.render.helm.includeCRDs ?? false,
          values: m.render.helm.values ?? {},
        },
      }
    : undefined,
  patches: (m.patches ?? []).map((p) => ({
    target: { ...p.target },
    set: { ...p.set },
  })),
  overrides: {
    values: {
      policy: m.overrides.values.policy,
      allowed: m.overrides.values.allowed ? [...m.overrides.values.allowed] : undefined,
    },
    patches: {
      policy: m.overrides.patches.policy,
      allowed: m.overrides.patches.allowed?.map((a) => ({
        target: { ...a.target },
        paths: [...a.paths],
      })),
    },
  },
  defaults: { ...m.defaults },
})
