import type { ArtifactNode, Verification } from '../../api/types'
import { signatures } from './artifactKinds'

/** The icon a signer is drawn with. */
export type SignerIcon = 'key' | 'github' | 'gitlab' | 'person' | 'identity'

/** A signer, readable at a glance. */
export interface Signer {
  icon: SignerIcon
  keyless: boolean
  /** Who signed, short: a key's name, a repository, an email address. */
  title: string
  /** What else tells them apart: the workflow and ref, or the key fingerprint. */
  detail?: string
  /** The full identity: the key ID, or the subject and issuer. */
  full: string
}

const githubActions = 'https://token.actions.githubusercontent.com'
const gitlab = 'https://gitlab.com'

/** refs/tags/v1 → v1, refs/heads/main → main. */
function shortRef(ref: string): string {
  return ref.replace(/^refs\/(tags|heads)\//, '')
}

function host(url?: string): string {
  if (!url) return ''
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

/**
 * describeSigner turns who signed into something readable: a GitHub or
 * GitLab workflow by its repository, an e-mail identity, or a key by its
 * name. keySource names where a key not trusted by the Kitchen comes from,
 * such as the Secret of a Product's verify.
 */
export function describeSigner(v: Pick<Verification, 'keyID' | 'keyName' | 'issuer' | 'subject'>, keySource?: string): Signer {
  if (v.subject) {
    const full = `${v.subject}\nissuer ${v.issuer ?? 'unknown'}`
    if (v.issuer === githubActions) {
      const m = /^https:\/\/github\.com\/([^/]+\/[^/]+)\/\.github\/workflows\/([^@]+)@(.+)$/.exec(v.subject)
      if (m) return { icon: 'github', keyless: true, title: m[1], detail: `${m[2]} · ${shortRef(m[3])}`, full }
    }
    if (v.issuer === gitlab) {
      const m = /^https:\/\/gitlab\.com\/(.+?)\/\/(.+)@(.+)$/.exec(v.subject)
      if (m) return { icon: 'gitlab', keyless: true, title: m[1], detail: `${m[2]} · ${shortRef(m[3])}`, full }
    }
    if (/^[^/\s]+@[^/\s]+$/.test(v.subject)) {
      return { icon: 'person', keyless: true, title: v.subject, detail: host(v.issuer), full }
    }
    return { icon: 'identity', keyless: true, title: v.subject.replace(/^https?:\/\//, ''), detail: host(v.issuer), full }
  }

  const fingerprint = v.keyID ? `${v.keyID.slice(0, 8)}…` : undefined
  const full = `key ${v.keyID ?? 'unknown'}`
  if (v.keyName === 'current') return { icon: 'key', keyless: false, title: 'Kokumi key', detail: fingerprint, full }
  if (v.keyName) return { icon: 'key', keyless: false, title: v.keyName, detail: fingerprint, full }
  return { icon: 'key', keyless: false, title: keySource ?? 'Public key', detail: fingerprint, full }
}

/**
 * Who signed node: its own signer when it is a verified signature, else the
 * signers of the verified signatures attached to it.
 */
export function signersOf(node: ArtifactNode, keySource?: string): Signer[] {
  const verifications = node.verification?.verified ? [node.verification] : signatures(node)
  return verifications.map((v) => describeSigner(v, keySource))
}
