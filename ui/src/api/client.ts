import { authHeaders, refresh } from './auth'
import type { Order, Preparation, OrderFormData, OrderPreviewRequest, Product, ProductFormData, ProductPreviewRequest, Patch, Render, ChartInfo, Registry, RegistryFormData, Repository, RepositoryFormData, ArtifactInfo, ArtifactFile, Approval, ApprovalDecision, ArtifactConfig, ArtifactGraph, ProductSourceArtifact } from './types'

// All API calls are relative so they work both in dev (proxied by Vite) and
// in production (served from the same Go binary).
const BASE = '/api/v1'

// ── Helpers ──────────────────────────────────────────────────────────────────

// handleUnauthorized triggers a silent refresh on 401. If the refresh succeeds
// the caller should retry the original request; if it fails the token is
// cleared (via refresh()) and the app returns to the login screen. refresh()
// itself collapses concurrent callers into a single in-flight request.
async function handleUnauthorized(status: number): Promise<boolean> {
  if (status !== 401) return false
  return refresh()
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json', ...authHeaders(), ...init?.headers },
    ...init,
  })

  if (!res.ok) {
    const recovered = await handleUnauthorized(res.status)
    if (recovered) {
      // Retry once with the freshly issued access token.
      const retry = await fetch(`${BASE}${path}`, {
        headers: { 'Content-Type': 'application/json', ...authHeaders(), ...init?.headers },
        ...init,
      })
      if (retry.ok) {
        if (retry.status === 204) return undefined as T
        return retry.json() as Promise<T>
      }
    }
    let message = `HTTP ${res.status}`
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // ignore parse errors
    }
    throw new Error(message)
  }

  // 204 No Content has no body.
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

// ── Orders ────────────────────────────────────────────────────────────────────

export function listOrders(): Promise<Order[]> {
  return request<Order[]>('/orders')
}

export function getOrder(namespace: string, name: string): Promise<Order> {
  return request<Order>(`/orders/${namespace}/${name}`)
}

/** Helm values for a request body; empty values are omitted. */
function valuesBody(values?: Record<string, unknown>): Record<string, unknown> | undefined {
  return values && Object.keys(values).length > 0 ? values : undefined
}

export function createOrder(data: OrderFormData, commitMessage?: string): Promise<Order> {
  // Edits are only set via the manifest editor on existing orders, never on create.
  return request<Order>('/orders', {
    method: 'POST',
    body: JSON.stringify({
      namespace: data.namespace,
      name: data.name,
      productRef: data.productRef,
      values: valuesBody(data.values),
      destination: data.destination,
      registryRefs: data.registryRefs,
      patches: data.patches,
      mode: data.mode,
      approvals: data.approvals,
      commitMessage: commitMessage ?? '',
    }),
  })
}

/** Replaces the spec of an Order. Its Product cannot be changed. */
export function updateOrder(
  namespace: string,
  name: string,
  data: OrderFormData,
  commitMessage?: string,
): Promise<Order> {
  return request<Order>(`/orders/${namespace}/${name}`, {
    method: 'PUT',
    body: JSON.stringify({
      values: valuesBody(data.values),
      destination: data.destination,
      registryRefs: data.registryRefs,
      patches: data.patches,
      edits: data.edits,
      mode: data.mode,
      approvals: data.approvals,
      commitMessage: commitMessage ?? '',
    }),
  })
}

export function deleteOrder(namespace: string, name: string): Promise<void> {
  return request<void>(`/orders/${namespace}/${name}`, { method: 'DELETE' })
}

export function saveOrderEdits(
  namespace: string,
  name: string,
  edits: Patch[],
  commitMessage?: string,
): Promise<Order> {
  return request<Order>(`/orders/${namespace}/${name}/edits`, {
    method: 'PUT',
    body: JSON.stringify({ edits, commitMessage: commitMessage ?? '' }),
  })
}

export function getDefaultRegistry(): Promise<{ baseURL: string }> {
  return request<{ baseURL: string }>('/oci/default')
}

/**
 * The Registries a read of any OCI location connects through: those of the
 * namespace named in registryRefs, the longest matching url winning. Without
 * one the location is read anonymously; a user who may not get a named
 * Registry's Secret is refused, never read anonymously.
 */
export interface OCIReadScope {
  namespace?: string
  registryRefs?: string[]
}

function ociQuery(params: Record<string, string>, scope?: OCIReadScope): string {
  const q = new URLSearchParams(params)
  if (scope?.namespace) q.set('namespace', scope.namespace)
  if (scope?.registryRefs?.length) q.set('registryRefs', scope.registryRefs.join(','))
  return q.toString()
}

export function listOCITags(ref: string, scope?: OCIReadScope): Promise<string[]> {
  return request<{ tags: string[] }>(`/oci/tags?${ociQuery({ ref }, scope)}`).then((r) => r.tags)
}

export function getArtifactInfo(ref: string, version: string, scope?: OCIReadScope): Promise<ArtifactInfo> {
  return request<ArtifactInfo>(`/oci/artifact?${ociQuery({ ref, version }, scope)}`)
}

export function getChartInfo(ref: string, version: string, scope?: OCIReadScope): Promise<ChartInfo> {
  return request<ChartInfo>(`/oci/chart-info?${ociQuery({ ref, version }, scope)}`)
}

/** The read scope of a Repository: its namespace and its Registry. */
export function repositoryScope(r: Repository): OCIReadScope {
  return { namespace: r.namespace, registryRefs: r.registryRef ? [r.registryRef] : [] }
}

// ── Preparations ──────────────────────────────────────────────────────────────

export function listPreparations(
  namespace: string,
  orderName: string,
): Promise<Preparation[]> {
  return request<Preparation[]>(`/orders/${namespace}/${orderName}/preparations`)
}

// ── Previews (rendered on the server, nothing is pushed) ──────────────────────

/** Renders an Order over its Product into its files. */
export function previewOrder(req: OrderPreviewRequest): Promise<ArtifactFile[]> {
  return request<ArtifactFile[]>('/orders/preview', {
    method: 'POST',
    body: JSON.stringify(req),
  })
}

/** Renders a Product on its own into its files. */
export function previewProduct(req: ProductPreviewRequest): Promise<ArtifactFile[]> {
  return request<ArtifactFile[]>('/products/preview', {
    method: 'POST',
    body: JSON.stringify(req),
  })
}

/** Returns the files of a Preparation's rendered artifact. */
export function getManifest(namespace: string, prepName: string): Promise<ArtifactFile[]> {
  return request<ArtifactFile[]>(`/preparations/${namespace}/${prepName}/manifest`)
}

// ── Artifact graphs and intent ────────────────────────────────────────────────

/** Returns the graph of a Preparation's artifact: its manifest and everything attached to it. */
export function getPreparationArtifact(namespace: string, prepName: string): Promise<ArtifactGraph> {
  return request<ArtifactGraph>(`/preparations/${namespace}/${prepName}/artifact`)
}

/** Returns the graph of a Product's source and, when vendored, of the copy. */
export function getProductSourceArtifact(namespace: string, name: string): Promise<ProductSourceArtifact> {
  return request<ProductSourceArtifact>(`/products/${namespace}/${name}/source/artifact`)
}

/** Returns the graph of the artifact behind a tag of a Repository. */
export function getRepositoryArtifact(namespace: string, name: string, tag: string): Promise<ArtifactGraph> {
  return request<ArtifactGraph>(`/repositories/${namespace}/${name}/artifact?tag=${encodeURIComponent(tag)}`)
}

/** Returns the config blob of a Preparation's artifact. */
export function getIntent(namespace: string, prepName: string): Promise<ArtifactConfig> {
  return request<ArtifactConfig>(`/preparations/${namespace}/${prepName}/intent`)
}

// ── Approvals ─────────────────────────────────────────────────────────────────

export function listApprovals(namespace: string, prepName: string): Promise<Approval[]> {
  return request<Approval[]>(`/preparations/${namespace}/${prepName}/approvals`)
}

export function submitApproval(
  namespace: string,
  prepName: string,
  decision: ApprovalDecision,
  comment?: string,
): Promise<Approval> {
  return request<Approval>(`/preparations/${namespace}/${prepName}/approvals`, {
    method: 'POST',
    body: JSON.stringify({ decision, comment: comment ?? '' }),
  })
}

// ── Promote ───────────────────────────────────────────────────────────────────

export function promote(
  namespace: string,
  orderName: string,
  preparation: string,
): Promise<{ serving: string }> {
  return request<{ serving: string }>(
    `/orders/${namespace}/${orderName}/promote`,
    {
      method: 'POST',
      body: JSON.stringify({ preparation }),
    },
  )
}

// ── Products ──────────────────────────────────────────────────────────────────

export function listProducts(namespace?: string): Promise<Product[]> {
  return request<Product[]>(
    namespace ? `/products?namespace=${encodeURIComponent(namespace)}` : '/products',
  )
}

export function getProduct(namespace: string, name: string): Promise<Product> {
  return request<Product>(`/products/${namespace}/${name}`)
}

/** The spec fields of a Product request body. */
function productSpecBody(data: ProductFormData) {
  let render: Render | undefined = data.render
  if (render?.helm) render = { helm: { ...render.helm, values: valuesBody(render.helm.values) } }
  return {
    source: data.source,
    vendor: data.vendor,
    render,
    patches: data.patches,
    overrides: data.overrides,
    images: data.images,
    registryRefs: data.registryRefs,
  }
}

export function createProduct(data: ProductFormData): Promise<Product> {
  return request<Product>('/products', {
    method: 'POST',
    body: JSON.stringify({ name: data.name, namespace: data.namespace, ...productSpecBody(data) }),
  })
}

export function updateProduct(namespace: string, name: string, data: ProductFormData): Promise<Product> {
  return request<Product>(`/products/${namespace}/${name}`, {
    method: 'PUT',
    body: JSON.stringify(productSpecBody(data)),
  })
}

/** Deletes a Product; it stays until its Orders are deleted. */
export function deleteProduct(namespace: string, name: string): Promise<void> {
  return request<void>(`/products/${namespace}/${name}`, { method: 'DELETE' })
}

// ── Registries ────────────────────────────────────────────────────────────────

export function listRegistries(): Promise<Registry[]> {
  return request<Registry[]>('/registries')
}

/**
 * The request body of a Registry form. Typed-in credentials go into a
 * Secret the Registry owns; blank ones keep that Secret as it is.
 */
function registryBody(data: RegistryFormData, current?: Registry) {
  const body: Record<string, string> = { url: data.url, scheme: data.scheme, certSecretRef: data.certSecretRef }
  if (data.credentialMode === 'secretRef') {
    body.secretRef = data.secretRef
  } else if (data.credentialMode === 'direct') {
    if (data.username && data.password) {
      body.username = data.username
      body.password = data.password
    } else if (current?.secretRef) {
      body.secretRef = current.secretRef
    }
  }
  return body
}

export function createRegistry(data: RegistryFormData): Promise<Registry> {
  return request<Registry>('/registries', {
    method: 'POST',
    body: JSON.stringify({ name: data.name, namespace: data.namespace, ...registryBody(data) }),
  })
}

export function updateRegistry(current: Registry, data: RegistryFormData): Promise<Registry> {
  return request<Registry>(`/registries/${current.namespace}/${current.name}`, {
    method: 'PUT',
    body: JSON.stringify(registryBody(data, current)),
  })
}

/** Deletes a Registry and the credentials Secret the UI stored for it. */
export function deleteRegistry(namespace: string, name: string): Promise<void> {
  return request<void>(`/registries/${namespace}/${name}`, { method: 'DELETE' })
}

// ── Repositories ──────────────────────────────────────────────────────────────

export function listRepositories(): Promise<Repository[]> {
  return request<Repository[]>('/repositories')
}

export function createRepository(data: RepositoryFormData): Promise<Repository> {
  return request<Repository>('/repositories', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export function updateRepository(namespace: string, name: string, data: RepositoryFormData): Promise<Repository> {
  return request<Repository>(`/repositories/${namespace}/${name}`, {
    method: 'PUT',
    body: JSON.stringify({ url: data.url, registryRef: data.registryRef, description: data.description }),
  })
}

export function deleteRepository(namespace: string, name: string): Promise<void> {
  return request<void>(`/repositories/${namespace}/${name}`, { method: 'DELETE' })
}

// ── Settings (singleton Kitchen) ──────────────────────────────────────────────

export interface SecretRef {
  name: string
}

export interface AdminUserSettings {
  enabled?: boolean
  username?: string
  secretRef?: SecretRef
}

export interface OIDCSettings {
  issuerURL: string
  clientID: string
  clientSecretRef?: SecretRef
  usernameClaim?: string
  emailClaim?: string
  groupsClaim?: string
  scopes?: string[]
}

export interface AuthSettings {
  adminUser?: AdminUserSettings
  oidc?: OIDCSettings
  tokenSigningKeySecretRef?: SecretRef
}

/** A public key Kokumi trusts for its own artifacts besides its signing key. */
export interface TrustedKey {
  name: string
  publicKey: string
}

export interface SigningSettings {
  trustedKeys: TrustedKey[]
  /** Every Product source must verify. */
  requireSourceVerification: boolean
}

/** Redirects image lookups and copies under a repository prefix to a mirror. */
export interface ImageMirror {
  /** A normalized repository prefix, e.g. docker.io or docker.io/library. */
  prefix: string
  /** The oci:// URL that replaces the prefix, e.g. oci://harbor.example.com/dockerhub. */
  location: string
}

export interface ImagesSettings {
  mirrors: ImageMirror[]
}

export interface Settings {
  argoCDURL: string
  auth?: AuthSettings
  signing: SigningSettings
  images: ImagesSettings
}

export function getSettings(): Promise<Settings> {
  return request<Settings>('/settings')
}

export function saveSettings(argoCDURL: string): Promise<Settings> {
  return request<Settings>('/settings', {
    method: 'PUT',
    body: JSON.stringify({ argoCDURL }),
  })
}

// saveAuthSettings replaces the whole auth subtree; argoCDURL is omitted so
// the server's merge semantics leave the General tab's value untouched.
export function saveAuthSettings(auth: AuthSettings): Promise<Settings> {
  return request<Settings>('/settings', {
    method: 'PUT',
    body: JSON.stringify({ auth }),
  })
}

// saveImagesSettings replaces the whole images subtree.
export function saveImagesSettings(images: ImagesSettings): Promise<Settings> {
  return request<Settings>('/settings', {
    method: 'PUT',
    body: JSON.stringify({ images }),
  })
}

// saveSigningSettings replaces the whole signing subtree.
export function saveSigningSettings(signing: SigningSettings): Promise<Settings> {
  return request<Settings>('/settings', {
    method: 'PUT',
    body: JSON.stringify({ signing }),
  })
}

// ── Signing (public keys; readable without login) ─────────────────────────────

/** A public key that verifies Kokumi's signatures. */
export interface SigningKey {
  /** "current" for the signing key, else the trusted key's name. */
  name: string
  /** Missing for a trusted key that does not parse. */
  keyID: string
  publicKey: string
}

export interface SigningInfo {
  /** The key Kokumi signs with; absent until the controller loaded it. */
  current?: SigningKey
  ready: boolean
  message?: string
  trustedKeys: SigningKey[]
  requireSourceVerification: boolean
}

export function getSigning(): Promise<SigningInfo> {
  return request<SigningInfo>('/signing')
}

// ── WhoAmI (identity + capabilities that decide which features are offered) ──

export interface WhoAmI {
  subject: string
  username?: string
  email?: string
  provider?: string
  groups?: string[]
  /** Capability names; see mergeCapabilities in ./capabilities. */
  capabilities: string[]
  /** Capabilities whose checks failed; not part of capabilities. */
  undetermined?: string[]
}

export function getWhoAmI(): Promise<WhoAmI> {
  return request<WhoAmI>('/whoami')
}

/** Capabilities that hold per namespace, which whoami does not report. */
export interface NamespaceCapabilities {
  namespace: string
  capabilities: string[]
  undetermined?: string[]
}

export function getNamespaceCapabilities(namespace: string): Promise<NamespaceCapabilities> {
  return request<NamespaceCapabilities>(`/namespaces/${encodeURIComponent(namespace)}/capabilities`)
}
