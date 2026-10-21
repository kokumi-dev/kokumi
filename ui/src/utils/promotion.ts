import type { DeploymentEntry, Order, Preparation, Serving, UpstreamStage } from '../api/types'
import type { Tone } from '../components/shared/tone'
import { formatDuration } from './format'

/** The deployment a Serving deploys: the newest entry of its history whose promotion is recorded. */
export function deployedEntry(serving?: Serving): DeploymentEntry | undefined {
  return serving?.history?.find((e) => !!e.promotionRecord)
}

/** The Serving of an Order, which carries the Order's name. */
export function servingOf(order: { namespace: string; name: string }, servings: Serving[] | null): Serving | undefined {
  return servings?.find((s) => s.namespace === order.namespace && s.name === order.name)
}

/** The first 12 hex digits of a Product revision, as Kokumi names Preparations after digests. */
export function shortRevision(revision?: string): string {
  return (revision ?? '').replace('sha256:', '').slice(0, 12)
}

/** How a soak reads, e.g. "a soak of 1 day", "no soak". */
export function soakLabel(seconds?: number): string {
  return seconds ? `a soak of ${formatDuration(seconds)}` : 'no soak'
}

/** Whether the time at iso comes within a minute of now, too soon for a relative time to tell. */
export function withinAMinute(iso: string, now: number = Date.now()): boolean {
  return new Date(iso).getTime() - now < 60_000
}

/** The outcome of the upstream gate, as the UpstreamDeployed condition reports it. */
export type UpstreamOutcome = 'Satisfied' | 'Soaking' | 'NotDeployedUpstream' | 'InvalidPipeline'

/** What one upstream stage says about a revision. */
export interface StageVerdict {
  stage: UpstreamStage
  outcome: UpstreamOutcome
  text: string
  /** When the soak under way ends. */
  soakEndsAt?: string
}

/** The verdict of a Preparation's upstream gate. */
export interface UpstreamVerdict {
  tone: Tone
  outcome: UpstreamOutcome
  text: string
  /** False while the gate holds the Preparation back. */
  passes: boolean
  stages: StageVerdict[]
}

const severity: Record<UpstreamOutcome, number> = { Satisfied: 0, Soaking: 1, NotDeployedUpstream: 2, InvalidPipeline: 3 }

const tones: Record<UpstreamOutcome, Tone> = {
  Satisfied: 'success',
  Soaking: 'warning',
  NotDeployedUpstream: 'warning',
  InvalidPipeline: 'danger',
}

/**
 * What an upstream stage says about a revision, as the Serving controller
 * decides it: the newest deployment of the revision that was healthy for the
 * stage's soak time, without degrading, and whose deployment record is
 * pushed qualifies it, also after the stage moved on.
 */
export function stageVerdict(
  stage: UpstreamStage,
  revision: string,
  product: string,
  namespace: string,
  orders: Order[],
  servings: Serving[] | null,
  now: number = Date.now(),
): StageVerdict {
  const upstream = orders.find((o) => o.namespace === namespace && o.name === stage.order)
  if (!upstream) return { stage, outcome: 'NotDeployedUpstream', text: `${stage.order} does not exist` }
  if (upstream.productRef.name !== product) {
    return { stage, outcome: 'InvalidPipeline', text: `${stage.order} is a variant of Product ${upstream.productRef.name}` }
  }

  const soak = (stage.soakSeconds ?? 0) * 1000
  const history = servingOf(upstream, servings)?.history ?? []
  let soaking: StageVerdict | undefined
  for (const e of history) {
    if (e.productRevision !== revision || !e.promotionRecord || !e.healthyAt || e.endReason === 'Degraded') continue
    const healthy = new Date(e.healthyAt).getTime()
    const healthyFor = (e.endedAt ? new Date(e.endedAt).getTime() : now) - healthy
    if (healthyFor >= soak && e.deploymentRecord) {
      return { stage, outcome: 'Satisfied', text: `${stage.order} deployed it` }
    }
    if (!soaking && healthyFor >= soak) {
      soaking = { stage, outcome: 'Soaking', text: `${stage.order} is recording its deployment` }
    } else if (!soaking && !e.endedAt) {
      const endsAt = new Date(healthy + soak).toISOString()
      soaking = { stage, outcome: 'Soaking', text: `${stage.order} runs it; ${soakLabel(stage.soakSeconds)}`, soakEndsAt: endsAt }
    }
  }
  return soaking ?? { stage, outcome: 'NotDeployedUpstream', text: `${stage.order} has not deployed it healthily` }
}

/**
 * The verdict of the upstream gate of prep, in the order the Serving
 * controller decides it; null when it names no upstream stages. A
 * Preparation its Order's Serving promoted before passes again.
 */
export function upstreamVerdict(
  prep: Preparation,
  order: Order | undefined,
  orders: Order[] | null,
  servings: Serving[] | null,
  now: number = Date.now(),
): UpstreamVerdict | null {
  const stages = prep.after ?? []
  if (stages.length === 0) return null
  const own = servingOf({ namespace: prep.namespace, name: prep.order }, servings)
  if (own?.history?.some((e) => e.preparation === prep.name && !!e.promotionRecord)) {
    return { tone: 'success', outcome: 'Satisfied', text: 'Promoted here', passes: true, stages: [] }
  }
  if (!order || !orders) return null

  const verdicts = stages.map((s) => stageVerdict(s, prep.productRevision, order.productRef.name, prep.namespace, orders, servings, now))
  const worst = verdicts.reduce<UpstreamOutcome>((w, v) => (severity[v.outcome] > severity[w] ? v.outcome : w), 'Satisfied')
  const text =
    worst === 'Satisfied'
      ? `Deployed upstream by ${stages.map((s) => s.order).join(', ')}`
      : verdicts
          .filter((v) => v.outcome === worst)
          .map((v) => v.text)
          .join('; ')
  return { tone: tones[worst], outcome: worst, text, passes: worst === 'Satisfied', stages: verdicts }
}

/** A stage of a pipeline: an Order, how deep it runs, and the Orders it runs after. */
export interface PipelineStage {
  order: Order
  /** 0 for a stage without upstream stages, else one more than its deepest upstream stage. */
  depth: number
}

/**
 * The stages of the pipeline of order: the Orders of its Product in its
 * namespace that it runs after, or that run after it, directly or not,
 * ordered by depth and name.
 */
export function pipelineStages(order: Order, orders: Order[]): PipelineStage[] {
  const variants = orders.filter((o) => o.namespace === order.namespace && o.productRef.name === order.productRef.name)
  const byName = new Map(variants.map((o) => [o.name, o]))
  const upstreams = (o: Order) => (o.after ?? []).map((s) => s.order).filter((n) => byName.has(n))

  const connected = new Set<string>([order.name])
  for (let grew = true; grew; ) {
    grew = false
    for (const o of variants) {
      const linked = connected.has(o.name)
        ? upstreams(o).filter((n) => !connected.has(n))
        : upstreams(o).some((n) => connected.has(n))
          ? [o.name]
          : []
      for (const n of linked) {
        connected.add(n)
        grew = true
      }
    }
  }

  const depths = new Map<string, number>()
  const depth = (name: string, visiting: Set<string>): number => {
    const known = depths.get(name)
    if (known !== undefined) return known
    if (visiting.has(name)) return 0 // a cycle, which the Order reports
    visiting.add(name)
    const d = Math.max(-1, ...upstreams(byName.get(name)!).map((n) => depth(n, visiting))) + 1
    visiting.delete(name)
    depths.set(name, d)
    return d
  }

  return [...connected]
    .map((name) => ({ order: byName.get(name)!, depth: depth(name, new Set()) }))
    .sort((a, b) => a.depth - b.depth || a.order.name.localeCompare(b.order.name))
}

/** A promotion the Serving holds until the gates of its Preparation pass. */
export interface QueuedPromotion {
  serving: Serving
  /** The gate it waits for, e.g. "upstream stages". */
  waitingFor: string
  /** Why, as the gate's condition says. */
  message: string
}

/** The gates in the order the Serving checks them, with how the UI names them. */
const gates: [type: string, label: string][] = [
  ['UpstreamDeployed', 'upstream stages'],
  ['ChecksPassed', 'checks'],
  ['Approved', 'approvals'],
  ['Verified', 'verification'],
]

/**
 * The queued promotion of prep: it is promoted to its Order's Serving, which
 * does not deploy it yet. A promotion is an intention; the Serving deploys it
 * once its gates pass. Only Manual Orders promote; callers check the mode.
 * Undefined when prep is not queued.
 */
export function queuedPromotion(prep: Preparation, servings: Serving[] | null): QueuedPromotion | undefined {
  const serving = servingOf({ namespace: prep.namespace, name: prep.order }, servings)
  return serving?.desiredPreparation === prep.name ? servingQueue(serving) : undefined
}

/**
 * The promotion queued on serving: the Preparation promoted to it that it
 * does not deploy yet, and the gate that holds it. Only Manual Orders
 * promote; callers check the mode. Undefined when nothing is queued.
 */
export function servingQueue(serving: Serving): QueuedPromotion | undefined {
  const name = serving.desiredPreparation
  if (!name || serving.deployedPreparation === name) return undefined
  return { serving, ...(heldBy(serving, name) ?? { waitingFor: 'the deployment to start', message: 'The Serving starts deploying it' }) }
}

/**
 * The first gate of serving that holds the Preparation name, and why;
 * undefined when none does. The gate conditions describe the Preparation the
 * Serving converges to, and name it first.
 */
function heldBy(serving: Serving, name: string): Pick<QueuedPromotion, 'waitingFor' | 'message'> | undefined {
  const prefix = `${name}: `
  for (const [type, label] of gates) {
    const c = serving.conditions?.find((c) => c.type === type)
    if (c && c.status !== 'True' && c.message?.startsWith(prefix)) {
      return { waitingFor: label, message: c.message.slice(prefix.length) }
    }
  }
  return undefined
}

/** Why the newest Preparation of an Order is not deployed yet. */
export interface PendingUpdate {
  /** The newest Preparation. */
  preparation: string
  /**
   * queued: promoted to the Serving of a Manual Order, which holds it.
   * waiting: the Serving of an Automatic Order holds it.
   * unpromoted: nobody promoted it to the Serving of a Manual Order yet.
   */
  kind: 'queued' | 'waiting' | 'unpromoted'
  /**
   * The gate that holds it, e.g. "approvals", or would hold it once
   * promoted; undefined while it deploys, or when a promotion would deploy
   * it at once.
   */
  waitingFor?: string
  /** Why, as the gate says. */
  reason?: string
}

/**
 * Why the newest Preparation of order is not deployed: the gate its Serving
 * holds it at or, under Manual, that nobody promoted it yet and whether its
 * upstream stages would hold it. latest is that Preparation, orders the
 * Orders of its Product. Undefined when the Order deploys its newest
 * Preparation or has none.
 */
export function pendingUpdate(
  order: Order,
  latest: Preparation | undefined,
  orders: Order[] | null,
  servings: Serving[] | null,
): PendingUpdate | undefined {
  const name = order.latestRevision
  if (!name || name === order.activePreparation) return undefined

  const serving = servingOf(order, servings)
  if (order.mode === 'Manual' && serving?.desiredPreparation !== name) {
    const verdict = latest ? upstreamVerdict(latest, order, orders, servings) : null
    return verdict && !verdict.passes
      ? { preparation: name, kind: 'unpromoted', waitingFor: 'upstream stages', reason: verdict.text }
      : { preparation: name, kind: 'unpromoted' }
  }
  const held = serving && heldBy(serving, name)
  return { preparation: name, kind: order.mode === 'Manual' ? 'queued' : 'waiting', waitingFor: held?.waitingFor, reason: held?.message }
}

/** A pending update in a few words that fit a card, e.g. "Queued · approvals". */
export function pendingLabel(pending: PendingUpdate): string {
  if (pending.kind === 'unpromoted') return 'Not promoted'
  if (!pending.waitingFor) return 'Deploying'
  const gate = pending.waitingFor === 'upstream stages' ? 'upstream' : pending.waitingFor
  return pending.kind === 'queued' ? `Queued · ${gate}` : `Waits for ${gate}`
}

/** A pending update in a sentence, e.g. "Its latest Preparation waits for approvals: 0 of 1 required approvals." */
export function pendingText(pending: PendingUpdate): string {
  const reason = pending.reason?.replace(/\.$/, '')
  switch (pending.kind) {
    case 'unpromoted':
      return reason
        ? `Its latest Preparation is not promoted. Promoted, it waits for upstream stages: ${reason}.`
        : 'Its latest Preparation is not promoted yet.'
    case 'queued':
      return pending.waitingFor
        ? `Its latest Preparation is promoted and waits for ${pending.waitingFor}: ${reason}.`
        : 'Its latest Preparation is promoted and deploying.'
    case 'waiting':
      return pending.waitingFor ? `Its latest Preparation waits for ${pending.waitingFor}: ${reason}.` : 'Its latest Preparation is deploying.'
  }
}

/** What a person still has to do before a Preparation can be deployed. */
export interface HumanSteps {
  /** Its Manual Order's Serving is not pointed at it yet. */
  promote: boolean
  /** Its approval policy is not satisfied yet; undefined without a policy or once approved. */
  approval?: {
    /** How many more eligible approvals it needs. */
    remaining: number
    /** An eligible approver requested changes. */
    changesRequested: boolean
    /** The groups whose members may approve. */
    groups: string[]
  }
}

/**
 * What a person still has to do before prep, the newest Preparation of
 * order, can be deployed: promote it under Manual, approve it under an
 * approval policy. Undefined when nobody has to act any more, and only the
 * Serving's own gates, such as soaks and checks, hold it.
 */
export function humanSteps(order: Order, prep: Preparation, servings: Serving[] | null): HumanSteps | undefined {
  const promote = order.mode === 'Manual' && !queuedPromotion(prep, servings) && !prep.isActive
  const a = prep.approval
  const approval =
    a && !a.approved
      ? {
          remaining: Math.max(0, a.requiredApprovals - a.approvedCount),
          changesRequested: a.state === 'ChangesRequested',
          groups: a.policy.allowedGroups,
        }
      : undefined
  return promote || approval ? { promote, approval } : undefined
}

/** What an approval policy still needs, e.g. "1 approval", "changes requested". */
export function approvalNeed(approval: NonNullable<HumanSteps['approval']>): string {
  if (approval.changesRequested) return 'changes requested'
  return `${approval.remaining} approval${approval.remaining === 1 ? '' : 's'}`
}

/**
 * Whether the viewer may review prep for its approval policy and still has
 * to: they are in an eligible group, and their latest vote is not an
 * approval. Advisory only; the Approval controller decides what counts.
 */
export function viewerMayApprove(prep: Preparation, viewer: { subject: string; groups: string[] } | null): boolean {
  const a = prep.approval
  if (!a || !viewer || !viewer.groups.some((g) => a.policy.allowedGroups.includes(g))) return false
  return !a.votes?.some((v) => v.subject === viewer.subject && v.decision === 'Approve')
}
