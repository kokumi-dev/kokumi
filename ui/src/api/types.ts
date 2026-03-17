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

export interface Order {
  name: string
  namespace: string
  labels?: Record<string, string>
  source: OCISource
  destination: OCIDestination
  render?: Render
  patches?: Patch[]
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

// ── Form data types ───────────────────────────────────────────────────────────

export interface OrderFormData {
  name: string
  namespace: string
  source: OCISource
  destination: OCIDestination
  render?: Render
  patches: Patch[]
  autoDeploy: boolean
}

export const emptyOrderForm = (): OrderFormData => ({
  name: '',
  namespace: 'default',
  source: { oci: '', version: '' },
  destination: { oci: '' },
  render: undefined,
  patches: [],
  autoDeploy: false,
})

export const orderToFormData = (r: Order): OrderFormData => ({
  name: r.name,
  namespace: r.namespace,
  source: { ...r.source },
  destination: { ...r.destination },
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
  autoDeploy: r.autoDeploy,
})
