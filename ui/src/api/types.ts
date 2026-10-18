// ── Mirror of Go server DTOs ──────────────────────────────────────────────────

/** Names a Registry in the same namespace. */
export interface RegistryRef {
  name: string
}

/** Who accesses a registry location: Kokumi itself, Argo CD, or the kubelet. */
export type RegistryClient = 'Kokumi' | 'ArgoCD' | 'Kubelet'

/**
 * A registry location an object needs, who reads or writes it, and the
 * Registry Kokumi connects there through. Entries of Argo CD and the kubelet
 * only report what they need; Kokumi does not configure them.
 */
export interface RegistryAccess {
  /** A location prefix, e.g. oci://ghcr.io/org/app/images. */
  url: string
  operation: 'Pull' | 'Push'
  client: RegistryClient
  /** Absent when the location is accessed without a Registry. */
  registry?: string
}

export interface OCISource {
  url: string
  version: string
  /** Requires the source to carry a trusted signature before it is rendered. */
  verify?: SourceVerify
}

/** A keyless signer a source may be signed by; both fields are RE2 regular expressions. */
export interface OIDCIdentityMatch {
  issuer: string
  subject: string
}

/** How a source's signature is verified: by the keys in a Secret, or keyless against signer identities. */
export interface SourceVerify {
  /** Secret in the Product's namespace whose *.pub keys are trusted. */
  secretRef?: string
  matchOIDCIdentity?: OIDCIdentityMatch[]
  /** Secret holding a Sigstore trusted_root.json; needed for keyless verification without network access. */
  trustedRootSecretRef?: string
}

/** Who signed a source whose signature verified. */
export interface SourceVerification {
  provider: string
  /** ID of the public key, for key-based signatures. */
  keyID?: string
  /** OIDC issuer and subject of a keyless signer. */
  issuer?: string
  subject?: string
}

export interface OCIDestination {
  url?: string
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
  releaseName?: string
  namespace?: string
  includeCRDs?: boolean
  values?: Record<string, unknown>
}

export type FileLayout = 'Single' | 'Multi'

export interface ManifestRender {
  layout?: FileLayout
}

export interface Render {
  helm?: HelmRender
  manifest?: ManifestRender
}

export interface Condition {
  type: string
  status: string
  reason?: string
  message?: string
  lastTransitionTime?: string
}

export interface ProductRef {
  name: string
}

/** A variant of exactly one Product; only holds what differs from it. */
export interface Order {
  name: string
  namespace: string
  labels?: Record<string, string>
  productRef: ProductRef
  /** Helm values; only valid when the Product renders with Helm. */
  values?: Record<string, unknown>
  destination: OCIDestination
  effectiveDestination?: string
  /** Registries that authenticate the destination; the longest matching url wins. */
  registryRefs?: RegistryRef[]
  registryAccess?: RegistryAccess[]
  patches?: Patch[]
  edits?: Patch[]
  mode: PromotionMode
  approvals?: ApprovalPolicy
  state: string
  latestRevision?: string
  activePreparation?: string
  conditions?: Condition[]
  createdAt?: string
}

export type PromotionMode = 'Automatic' | 'Manual'

export interface ApprovalPolicy {
  requiredApprovals: number
  allowedGroups: string[]
}

export type ApprovalDecision = 'Approve' | 'Reject'

export interface ApprovalVote {
  approvalName: string
  username?: string
  subject: string
  decision: ApprovalDecision
  result: 'Counted' | 'NotEligible'
  submittedAt: string
}

/** Approval gate summary; state is the reason of the Approved condition. */
export interface PreparationApproval {
  policy: ApprovalPolicy
  approved: boolean
  state: string
  message?: string
  requiredApprovals: number
  approvedCount: number
  rejectedCount: number
  ineligibleCount: number
  submissionCount: number
  votes?: ApprovalVote[]
  sealedAt?: string
  attestation?: string
}

export interface Approver {
  issuer: string
  subject: string
  username?: string
  email?: string
  groups?: string[]
}

export interface Approval {
  name: string
  namespace: string
  order: string
  preparation: string
  artifactDigest: string
  approver: Approver
  decision: ApprovalDecision
  comment?: string
  submittedAt: string
  counted: boolean
  reason?: string
  message?: string
}

export interface Artifact {
  ociRef: string
  digest: string
  /** The Registry the artifact was pushed through; absent without one. */
  registryRef?: string
}

export interface Preparation {
  name: string
  namespace: string
  order: string
  artifact: Artifact
  configHash: string
  state: string
  createdAt?: string
  isActive: boolean
  commitMessage?: string
  parentDigest?: string
  gitSource?: {
    repo?: string
    tag?: string
    commitHash?: string
    sourceLink?: {
      url: string
      label: string
    }
  }
  renderer: Renderer
  conditions?: Condition[]
  approval?: PreparationApproval
  /** The container images the manifests reference, as resolved when rendered. */
  images?: PreparationImage[]
}

/** A container image a Preparation's manifests reference. */
export interface PreparationImage {
  /** The reference as written in the manifests before any rewriting. */
  ref: string
  /** The normalized repository; absent when ref is not a valid reference. */
  name?: string
  /** Absent when the image could not be resolved, which only Record allows. */
  digest?: string
  mediaType?: string
  /** Where the manifests pull the image from after vendoring. */
  vendored?: string
}

/** What rendered a Preparation. The builder is reported by the controller itself. */
export interface Renderer {
  type: string
  version: string
  builder: string
}

/**
 * Whether a signature verified: a Sigstore bundle against the keys the
 * Kitchen trusts or, in the graph of a Product's source, any signature as
 * the Product controller verified it against spec.source.verify.
 */
export interface Verification {
  verified: boolean
  /** ID of the key that signed, for a key-based signature. */
  keyID?: string
  /** Name a Kitchen key is trusted under: "current" for the signing key, else the trusted key's name. */
  keyName?: string
  /** Issuer and subject of a keyless signer. */
  issuer?: string
  subject?: string
  /**
   * Why the signature is not verified: signed by a key or identity that is
   * not trusted (it may well be valid), invalid, in a format that is not
   * verified, or not checked by the Product controller yet.
   */
  reason?: 'UntrustedSigner' | 'Invalid' | 'Unsupported' | 'NotChecked'
  /** Why the signature did not verify. */
  error?: string
}

/** Content addressed by digest, as an OCI descriptor describes it. */
export interface Descriptor {
  mediaType: string
  /** For an image manifest without one, its config's media type. */
  artifactType?: string
  digest: string
  size: number
  annotations?: Record<string, string>
}

/**
 * How a node is attached to its parent: Subject for an OCI referrer, Tag for
 * cosign's legacy sha256-<hex>.sig/.att/.sbom tags, Index for a child of an
 * image index.
 */
export type ArtifactLink = 'Subject' | 'Tag' | 'Index'

/** One manifest of an artifact graph; its annotations are the manifest's own. */
export interface ArtifactNode extends Descriptor {
  /** Absent for the root. */
  link?: ArtifactLink
  /** The tag a node linked by Tag is stored under. */
  tag?: string
  config?: Descriptor
  layers?: Descriptor[]
  subject?: Descriptor
  /** Predicate type of a Sigstore bundle; for an unverified one, only what its annotation claims. */
  predicateType?: string
  /** Set for Sigstore bundles. Anyone who can push to the repository can attach a referrer; only verified ones are evidence. */
  verification?: Verification
  /** The signed in-toto statement; only set for verified bundles. */
  payload?: unknown
  /** Children of an image index. */
  manifests?: ArtifactNode[]
  /** Artifacts attached to this one, oldest first. */
  referrers?: ArtifactNode[]
  /** Children or referrers were left out at the limits of the walk. */
  truncated?: boolean
  /** Why children or referrers could not be read completely. */
  error?: string
}

/** An artifact with everything attached to it in its repository, read live from the registry. */
export interface ArtifactGraph {
  repository: string
  tag?: string
  /** Where the tag points now; differs from the root's digest when the tag moved. */
  tagDigest?: string
  root?: ArtifactNode
  /** Why the graph could not be read. */
  error?: string
}

/** A Product's source at its source location and, when vendored, the copy. */
export interface ProductSourceArtifact {
  source: ArtifactGraph
  vendored?: ArtifactGraph
}

/** The config blob of a Preparation's artifact: what was asked for and what it was built from. */
export interface ArtifactConfig {
  schemaVersion: number
  order?: { namespace: string; name: string; uid?: string }
  configHash?: string
  commitMessage?: string
  createdTime: string
  parent?: string
  source: { oci: string; version: string; digest: string; verified?: SourceVerification }
  destination: { oci: string }
  product?: { namespace: string; name: string; configHash?: string }
  renderer: Renderer
  intent?: unknown
  /** The images as resolved when rendered, with why an image could not be resolved. */
  images?: (PreparationImage & { error?: string })[]
}

export interface Serving {
  name: string
  namespace: string
  order: string
  desiredPreparation?: string
  targetPreparation?: string
  observedPreparation?: string
  deployedDigest?: string
  state: string
  conditions?: Condition[]
  createdAt?: string
}

// ── Product types ─────────────────────────────────────────────────────────────

/** Which changes Orders may make in one category: none, only the listed ones, or any. */
export type OverridePolicy = 'None' | 'Listed' | 'All'

export const overridePolicies: readonly OverridePolicy[] = ['None', 'Listed', 'All']

/** Reads a policy defensively: anything missing or unknown counts as None. */
export function overridePolicy(policy: string | undefined): OverridePolicy {
  return overridePolicies.find((p) => p === policy) ?? 'None'
}

/** A resource Orders may patch and edit; without a namespace it matches any namespace. */
export interface AllowedPatchTarget {
  target: PatchTarget
  /** JSONPaths Orders may set, e.g. .spec.replicas. */
  paths: string[]
}

/** Which Helm values Orders may set. */
export interface ValueOverrides {
  policy: OverridePolicy
  /** Exact dot paths, e.g. ui.message; only with policy Listed, and then not empty. */
  paths?: string[]
}

/** Which patches and manifest edits Orders may set. */
export interface PatchOverrides {
  policy: OverridePolicy
  /** Only with policy Listed, and then not empty. */
  targets?: AllowedPatchTarget[]
}

/** What the Orders of a Product may change; each category defaults to None. */
export interface Overrides {
  values: ValueOverrides
  patches: PatchOverrides
}

/** What Kokumi does with the container images of the rendered manifests. */
export type ImagePolicy = 'Record' | 'Pin' | 'Vendor'

export const imagePolicies: readonly ImagePolicy[] = ['Record', 'Pin', 'Vendor']

/** Reads an image policy defensively: anything missing or unknown counts as Record. */
export function imagePolicy(policy: string | undefined): ImagePolicy {
  return imagePolicies.find((p) => p === policy) ?? 'Record'
}

/** A field of a custom resource that holds an image reference, in kustomize's FieldSpec shape. */
export interface ImageFieldSpec {
  group?: string
  version?: string
  kind: string
  /** Slash-separated, with [] for list elements, e.g. spec/image. */
  path: string
}

/** How a Product's images are recorded, pinned or vendored. */
export interface ProductImages {
  policy: ImagePolicy
  fieldSpecs?: ImageFieldSpec[]
}

/** The source a Product's Orders render from: the vendored copy when vendoring is set. */
export interface ResolvedSource {
  url: string
  version: string
  digest?: string
  /** Set when the source's signature verified. */
  verified?: SourceVerification
}

/** The complete definition of an application; its Orders are variants of it. */
export interface Product {
  name: string
  namespace: string
  source: OCISource
  /**
   * Copies the source to this location, and vendored images below its
   * images/ path; {} is the in-cluster registry.
   */
  vendor?: OCIDestination
  render?: Render
  patches?: Patch[]
  overrides: Overrides
  images: ProductImages
  /** Registries that authenticate the source, the images and the vendor location. */
  registryRefs?: RegistryRef[]
  resolvedSource?: ResolvedSource
  registryAccess?: RegistryAccess[]
  state?: string
  conditions?: Condition[]
  createdAt?: string
  /** Set once the Product is deleted; it stays until its Orders are deleted. */
  deletionTimestamp?: string
}

// ── Registry and Repository types ─────────────────────────────────────────────

export type RegistryScheme = 'HTTPS' | 'HTTP'

/** How Kokumi connects to the locations below a URL prefix. Its credentials are never served. */
export interface Registry {
  name: string
  namespace: string
  /** A prefix: oci://host[:port][/path]. */
  url: string
  scheme: RegistryScheme
  /** A kubernetes.io/dockerconfigjson Secret in the Registry's namespace. */
  secretRef?: string
  /** A Secret with ca.crt, and tls.crt and tls.key for a client certificate. */
  certSecretRef?: string
  state: string
  conditions?: Condition[]
  createdAt?: string
}

/** A catalog entry: an OCI repository to browse and create Products from. */
export interface Repository {
  name: string
  namespace: string
  url: string
  /** The Registry the UI reads the repository through. */
  registryRef?: string
  description?: string
  state: string
  conditions?: Condition[]
  createdAt?: string
}

export interface ArtifactFile {
  path: string
  content: string
}

export interface ArtifactInfo {
  isHelm: boolean
  isManifest: boolean
  digest?: string
  files?: ArtifactFile[]
  chartInfo?: {
    name: string
    version: string
    appVersion?: string
    description?: string
    defaultValues?: string
    readme?: string
    hasSchema?: boolean
  }
}

// ── Registry / chart types ────────────────────────────────────────────────────

export interface ChartInfo {
  isHelm: boolean
  name: string
  description: string
  chartVersion: string
  /** YAML string of the chart's default values. */
  defaultValues: string
  /** Contents of README.md, empty when absent. */
  readme: string
  hasSchema: boolean
}

// ── Form data types ───────────────────────────────────────────────────────────

export interface OrderFormData {
  name: string
  namespace: string
  productRef: ProductRef
  /** Helm values; only sent when the Product renders with Helm. */
  values: Record<string, unknown>
  destination: OCIDestination
  registryRefs: RegistryRef[]
  patches: Patch[]
  edits: Patch[]
  mode: PromotionMode
  approvals?: ApprovalPolicy
}

export const emptyOrderForm = (product?: { namespace: string; name: string }): OrderFormData => ({
  name: '',
  namespace: product?.namespace ?? 'kokumi',
  productRef: { name: product?.name ?? '' },
  values: {},
  destination: {},
  registryRefs: [],
  patches: [],
  edits: [],
  mode: 'Manual',
})

const copyPatches = (patches?: Patch[]): Patch[] =>
  (patches ?? []).map((p) => ({ target: { ...p.target }, set: { ...p.set } }))

const copyRegistryRefs = (refs?: RegistryRef[]): RegistryRef[] => (refs ?? []).map((r) => ({ name: r.name }))

export const orderToFormData = (r: Order): OrderFormData => ({
  name: r.name,
  namespace: r.namespace,
  productRef: { name: r.productRef.name },
  values: r.values ?? {},
  destination: r.destination ? { ...r.destination } : {},
  registryRefs: copyRegistryRefs(r.registryRefs),
  patches: copyPatches(r.patches),
  edits: copyPatches(r.edits),
  mode: r.mode,
  approvals: r.approvals
    ? { requiredApprovals: r.approvals.requiredApprovals, allowedGroups: [...r.approvals.allowedGroups] }
    : undefined,
})

export interface ProductFormData {
  name: string
  namespace: string
  source: OCISource
  /** Absent: no vendoring; {} vendors into the in-cluster registry. */
  vendor?: OCIDestination
  render?: Render
  patches: Patch[]
  /** Lists are only set with policy Listed. */
  overrides: Overrides
  images: ProductImages
  registryRefs: RegistryRef[]
}

export const emptyProductForm = (): ProductFormData => ({
  name: '',
  namespace: 'kokumi',
  source: { url: '', version: '' },
  patches: [],
  overrides: { values: { policy: 'None' }, patches: { policy: 'None' } },
  images: { policy: 'Record' },
  registryRefs: [],
})

/** A copy of a Product's image settings that leaves out what is unset. */
const copyImages = (i?: ProductImages): ProductImages => ({
  policy: imagePolicy(i?.policy),
  ...(i?.fieldSpecs?.length ? { fieldSpecs: i.fieldSpecs.map((f) => ({ ...f })) } : {}),
})

/** A copy of a Product's overrides that keeps the lists only with policy Listed. */
const copyOverrides = (o?: Overrides): Overrides => {
  const values = overridePolicy(o?.values?.policy)
  const patches = overridePolicy(o?.patches?.policy)
  return {
    values: values === 'Listed' ? { policy: values, paths: [...(o?.values.paths ?? [])] } : { policy: values },
    patches: patches === 'Listed'
      ? {
          policy: patches,
          targets: (o?.patches.targets ?? []).map((a) => ({ target: { ...a.target }, paths: [...a.paths] })),
        }
      : { policy: patches },
  }
}

export const productToFormData = (p: Product): ProductFormData => ({
  name: p.name,
  namespace: p.namespace,
  source: {
    url: p.source.url,
    version: p.source.version,
    verify: p.source.verify
      ? {
          ...p.source.verify,
          matchOIDCIdentity: p.source.verify.matchOIDCIdentity?.map((m) => ({ ...m })),
        }
      : undefined,
  },
  vendor: p.vendor ? { ...p.vendor } : undefined,
  render: p.render?.helm
    ? { helm: { ...p.render.helm } }
    : p.render?.manifest
      ? { manifest: { ...p.render.manifest } }
      : undefined,
  patches: copyPatches(p.patches),
  overrides: copyOverrides(p.overrides),
  images: copyImages(p.images),
  registryRefs: copyRegistryRefs(p.registryRefs),
})

// ── Preview request types ─────────────────────────────────────────────────────

/** Renders an Order over its Product; nothing is pushed. */
export interface OrderPreviewRequest {
  namespace: string
  name: string
  productRef: ProductRef
  values?: Record<string, unknown>
  patches?: Patch[]
  edits?: Patch[]
}

/** Renders a Product, saved or not, without any Order on top. */
export interface ProductPreviewRequest {
  namespace: string
  name: string
  source: OCISource
  /** Where vendored images would be pulled from. */
  vendor?: OCIDestination
  render?: Render
  patches?: Patch[]
  images?: ProductImages
  registryRefs?: RegistryRef[]
}

/** How a Registry form stores credentials: typed in, or an existing Secret. */
export type CredentialMode = 'none' | 'direct' | 'secretRef'

export interface RegistryFormData {
  name: string
  namespace: string
  url: string
  scheme: RegistryScheme
  certSecretRef: string
  credentialMode: CredentialMode
  /** Stored in a Secret the Registry owns; blank keeps stored credentials. */
  username: string
  password: string
  secretRef: string
}

export const emptyRegistryForm = (): RegistryFormData => ({
  name: '',
  namespace: 'kokumi',
  url: '',
  scheme: 'HTTPS',
  certSecretRef: '',
  credentialMode: 'none',
  username: '',
  password: '',
  secretRef: '',
})

/** The Secret a Registry form stores typed-in credentials in. */
export const registryCredentialsSecret = (registryName: string) => `${registryName}-credentials`

export const registryToFormData = (r: Registry): RegistryFormData => ({
  name: r.name,
  namespace: r.namespace,
  url: r.url,
  scheme: r.scheme,
  certSecretRef: r.certSecretRef ?? '',
  credentialMode: !r.secretRef ? 'none' : r.secretRef === registryCredentialsSecret(r.name) ? 'direct' : 'secretRef',
  username: '',
  password: '',
  secretRef: r.secretRef ?? '',
})

export interface RepositoryFormData {
  name: string
  namespace: string
  url: string
  registryRef: string
  description: string
}

export const emptyRepositoryForm = (): RepositoryFormData => ({
  name: '',
  namespace: 'kokumi',
  url: '',
  registryRef: '',
  description: '',
})

export const repositoryToFormData = (r: Repository): RepositoryFormData => ({
  name: r.name,
  namespace: r.namespace,
  url: r.url,
  registryRef: r.registryRef ?? '',
  description: r.description ?? '',
})
